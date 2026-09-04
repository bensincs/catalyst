"""ASGI middleware and OTel hook that carry the Cortex header contract.

Two entry points read the ADR-2026-05-19 cross-service headers and bind the
resulting ``RequestContext`` for the duration of the request:

* :class:`RequestContextASGIMiddleware` — a generic ASGI middleware suitable
  for any Starlette or ASGI3 application. Use it when the application does
  *not* rely on :func:`cortex_otel.instrument_fastapi`.
* :func:`cortex_fastapi_server_request_hook` — an OTel FastAPI
  ``server_request_hook`` used by :func:`cortex_otel.instrument_fastapi`.
  FastAPIInstrumentor wraps ``OpenTelemetryMiddleware`` *outside* the user
  middleware stack by patching ``build_middleware_stack``, so an ASGI
  middleware cannot intercept the root request span. This hook fires when
  the root span is created and stamps the ctx.* attributes on the span
  directly, then binds the RequestContext for the request's asyncio task so
  any child spans and log records still pick it up.

Header contract:

======================  ==========================
Header                  Bound attribute
======================  ==========================
X-Request-Id            ctx.request_id
X-Ctx-Tenant            ctx.tenant
X-Ctx-App               ctx.app
X-Ctx-Workflow-Id       ctx.workflow_id
X-Ctx-Agent-Run-Id      ctx.agent_run_id
Authorization: Bearer   enduser.id_hash (sha256 of JWT ``sub``/``oid``)
======================  ==========================

Trace propagation continues to use the standard ``traceparent`` header
handled by the OTel propagator — do not read it here.

Wire in via Starlette/FastAPI::

    from cortex_otel import RequestContextASGIMiddleware
    app.add_middleware(RequestContextASGIMiddleware)

Or, for FastAPI apps using our instrumentation helper::

    from cortex_otel import instrument_fastapi
    instrument_fastapi(app)
"""

from __future__ import annotations

import base64
import hashlib
import json
from collections.abc import Awaitable, Callable
from typing import Any
from uuid import uuid4

from opentelemetry.trace import Span

from cortex_otel._context import (
    _CURRENT,
    RequestContext,
    context_to_attributes,
    request_context,
)

Scope = dict[str, Any]
Receive = Callable[[], Awaitable[dict[str, Any]]]
Send = Callable[[dict[str, Any]], Awaitable[None]]
ASGIApp = Callable[[Scope, Receive, Send], Awaitable[None]]


_HEADER_REQUEST_ID = b"x-request-id"
_HEADER_TENANT = b"x-ctx-tenant"
_HEADER_APP = b"x-ctx-app"
_HEADER_WORKFLOW_ID = b"x-ctx-workflow-id"
_HEADER_AGENT_RUN_ID = b"x-ctx-agent-run-id"
_HEADER_AUTHORIZATION = b"authorization"

# Gateway-issued trusted headers. The tenant ingress (Envoy Gateway SecurityPolicy
# HeadersToBackend) has ext-authz semantics that REPLACE any same-named client
# header, so if these are set they came from the gateway after JWT validation +
# hostname resolution — not from the caller. Prefer them over their untrusted
# X-Ctx-* counterparts to prevent a hostile client from poisoning ctx.tenant or
# ctx.app in downstream telemetry, dashboards, and cost accounting.
_HEADER_CORTEX_SUB = b"x-cortex-sub"
_HEADER_CORTEX_TENANT = b"x-cortex-tenant"
_HEADER_CORTEX_APP = b"x-cortex-app"

# Defence-in-depth cap on the Bearer token we're willing to base64+json decode.
# Upstream ingress already caps header size; this ensures a pathological value
# can't cost us CPU on every request even if that cap slips.
_MAX_TOKEN_LEN = 8 * 1024


# ASGI passes header values as raw bytes and, per RFC 7230, HTTP header values
# are ISO-8859-1 (latin-1) — a total mapping that cannot raise UnicodeDecodeError.
# Starlette itself decodes headers this way; keep the same convention here.
def _decode(value: bytes | None) -> str | None:
    if value is None:
        return None
    return value.decode("latin-1")


def _enduser_id_hash(authorization: bytes | None) -> str | None:
    """Return sha256 hex of the JWT subject, or None if it can't be extracted.

    Verification is upstream's job — this pulls the payload without checking
    the signature because the only downstream use is a stable pseudonym for
    telemetry. Any parse failure returns None so a malformed header can't
    take a request down.
    """

    if authorization is None:
        return None
    raw = _decode(authorization)
    if raw is None or not raw.lower().startswith("bearer "):
        return None
    token = raw[7:].strip()
    if not token or len(token) > _MAX_TOKEN_LEN:
        return None
    parts = token.split(".")
    if len(parts) < 2:
        return None
    payload_b64 = parts[1]
    padding = "=" * (-len(payload_b64) % 4)
    try:
        payload_bytes = base64.urlsafe_b64decode(payload_b64 + padding)
        payload = json.loads(payload_bytes)
    except ValueError:
        return None
    if not isinstance(payload, dict):
        return None
    subject = payload.get("sub") or payload.get("oid")
    if not isinstance(subject, str) or not subject:
        return None
    return hashlib.sha256(subject.encode("utf-8")).hexdigest()


def _request_context_from_scope(scope: Scope) -> RequestContext:
    """Read the Cortex header contract from an ASGI HTTP scope.

    Generates a canonical uuid4 request_id when the client did not supply one.
    Prefers the gateway-issued ``X-Cortex-*`` headers (trusted, set by the
    tenant ingress after JWT validation and hostname resolution) over the
    client-supplied ``X-Ctx-*`` counterparts. This keeps intra-mesh
    service-to-service propagation working via ``X-Ctx-*`` while making
    external callers unable to spoof tenant/app/identity.
    """

    headers: dict[bytes, bytes] = {}
    for key, value in scope.get("headers", []):
        headers.setdefault(key.lower(), value)

    tenant = _decode(headers.get(_HEADER_CORTEX_TENANT)) or _decode(headers.get(_HEADER_TENANT))
    app = _decode(headers.get(_HEADER_CORTEX_APP)) or _decode(headers.get(_HEADER_APP))

    # Prefer the pre-validated subject from the gateway; fall back to parsing
    # the JWT payload directly for callers not behind the ingress (tests,
    # local dev, service-to-service inside the mesh).
    cortex_sub = _decode(headers.get(_HEADER_CORTEX_SUB))
    if cortex_sub:
        enduser_id_hash: str | None = hashlib.sha256(cortex_sub.encode("utf-8")).hexdigest()
    else:
        enduser_id_hash = _enduser_id_hash(headers.get(_HEADER_AUTHORIZATION))

    return RequestContext(
        request_id=_decode(headers.get(_HEADER_REQUEST_ID)) or str(uuid4()),
        tenant=tenant,
        app=app,
        workflow_id=_decode(headers.get(_HEADER_WORKFLOW_ID)),
        agent_run_id=_decode(headers.get(_HEADER_AGENT_RUN_ID)),
        enduser_id_hash=enduser_id_hash,
    )


def cortex_fastapi_server_request_hook(span: Span, scope: Scope) -> None:
    """OTel FastAPI ``server_request_hook`` that stamps and binds ctx.*.

    Called by ``OpenTelemetryMiddleware`` when the root request span is
    created — which happens *outside* the FastAPI user middleware stack
    because ``FastAPIInstrumentor`` installs itself via a
    ``build_middleware_stack`` override. Reads the Cortex header contract
    from the ASGI scope, stamps the resulting attributes on the root span,
    and binds a RequestContext on the request's asyncio task so any child
    spans and log records emitted inside the handler inherit the same
    context via ``current_request_context()``.

    Binding is a bare ``ContextVar.set()`` with no matching reset: the
    binding lives for the lifetime of the request's asyncio task and is
    discarded when the task ends, so it cannot leak into unrelated code.
    """

    if not isinstance(scope, dict) or scope.get("type") != "http":
        return
    rc = _request_context_from_scope(scope)
    _CURRENT.set(rc)
    attrs = context_to_attributes(rc)
    if attrs and span.is_recording():
        span.set_attributes(attrs)


class RequestContextASGIMiddleware:
    """Bind Cortex per-request attributes from headers for the lifetime of a request.

    Non-HTTP scopes (``lifespan``, ``websocket``) pass through untouched.

    Safe to install alongside :func:`cortex_fastapi_server_request_hook` — the
    hook binds a RequestContext first, and this middleware's ``request_context``
    call merges the same values back in via its inherit-from-parent logic.
    """

    def __init__(self, app: ASGIApp) -> None:
        self.app = app

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return

        rc = _request_context_from_scope(scope)
        with request_context(
            request_id=rc.request_id,
            tenant=rc.tenant,
            app=rc.app,
            workflow_id=rc.workflow_id,
            agent_run_id=rc.agent_run_id,
            enduser_id_hash=rc.enduser_id_hash,
        ):
            await self.app(scope, receive, send)
