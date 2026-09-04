"""Per-request context propagation for Cortex OTel signals.

Cortex context attributes (``ctx.tenant``, ``ctx.app``, ``ctx.request_id``,
``ctx.agent_run_id``, ``ctx.workflow_id``, ``enduser.id_hash``) vary per
request — they cannot live on the OTel Resource. This module stashes them in
a ``ContextVar`` and stamps them onto every span and log emitted inside a
``request_context(...)`` block.

Typical usage from an ASGI middleware::

    from cortex_otel import request_context

    async def dispatch(scope, receive, send):
        with request_context(
            tenant=headers.get("x-ctx-tenant"),
            app=headers.get("x-ctx-app"),
            request_id=headers.get("x-request-id"),
        ):
            await app(scope, receive, send)
"""

from __future__ import annotations

from collections.abc import Iterator
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass

from opentelemetry.context import Context
from opentelemetry.sdk.trace import ReadableSpan, Span, SpanProcessor

from cortex_otel import _attributes as attrs


@dataclass(frozen=True)
class RequestContext:
    """Cortex per-request attribute bundle.

    Every field is optional so services can propagate only what they know at
    the request boundary.
    """

    app: str | None = None
    tenant: str | None = None
    request_id: str | None = None
    agent_run_id: str | None = None
    workflow_id: str | None = None
    enduser_id_hash: str | None = None


_CURRENT: ContextVar[RequestContext | None] = ContextVar(
    "cortex_otel.request_context", default=None
)


def current_request_context() -> RequestContext | None:
    """Return the RequestContext bound to the current async/thread scope, or None."""

    return _CURRENT.get()


@contextmanager
def request_context(
    *,
    app: str | None = None,
    tenant: str | None = None,
    request_id: str | None = None,
    agent_run_id: str | None = None,
    workflow_id: str | None = None,
    enduser_id_hash: str | None = None,
) -> Iterator[RequestContext]:
    """Bind Cortex per-request attributes to the current async/thread scope.

    Nested calls inherit any field left as ``None`` from the enclosing context;
    explicitly passing a value overrides it.
    """

    inherited = _CURRENT.get()
    merged = RequestContext(
        app=app if app is not None else (inherited.app if inherited else None),
        tenant=tenant if tenant is not None else (inherited.tenant if inherited else None),
        request_id=(
            request_id if request_id is not None else (inherited.request_id if inherited else None)
        ),
        agent_run_id=(
            agent_run_id
            if agent_run_id is not None
            else (inherited.agent_run_id if inherited else None)
        ),
        workflow_id=(
            workflow_id
            if workflow_id is not None
            else (inherited.workflow_id if inherited else None)
        ),
        enduser_id_hash=(
            enduser_id_hash
            if enduser_id_hash is not None
            else (inherited.enduser_id_hash if inherited else None)
        ),
    )
    token = _CURRENT.set(merged)
    try:
        yield merged
    finally:
        _CURRENT.reset(token)


def context_to_attributes(ctx: RequestContext) -> dict[str, str]:
    """Materialise a RequestContext into a flat ``{ADR-key: value}`` mapping.

    Only fields set on the RequestContext appear in the output — a caller who
    never bound ``workflow_id`` won't have ``ctx.workflow_id`` stamped on their
    signals.
    """

    out: dict[str, str] = {}
    if ctx.app is not None:
        out[attrs.CTX_APP] = ctx.app
    if ctx.tenant is not None:
        out[attrs.CTX_TENANT] = ctx.tenant
    if ctx.request_id is not None:
        out[attrs.CTX_REQUEST_ID] = ctx.request_id
    if ctx.agent_run_id is not None:
        out[attrs.CTX_AGENT_RUN_ID] = ctx.agent_run_id
    if ctx.workflow_id is not None:
        out[attrs.CTX_WORKFLOW_ID] = ctx.workflow_id
    if ctx.enduser_id_hash is not None:
        out[attrs.ENDUSER_ID_HASH] = ctx.enduser_id_hash
    return out


class RequestContextSpanProcessor(SpanProcessor):
    """Stamp ``ctx.*`` attributes on every span started inside ``request_context``.

    Registered ahead of the OTLP BatchSpanProcessor by ``setup_telemetry`` so
    attributes are attached before the span is queued for export.
    """

    def on_start(self, span: Span, parent_context: Context | None = None) -> None:
        ctx = _CURRENT.get()
        if ctx is None:
            return
        stamped = context_to_attributes(ctx)
        if stamped:
            span.set_attributes(stamped)

    def on_end(self, span: ReadableSpan) -> None:
        return

    def shutdown(self) -> None:
        return

    def force_flush(self, timeout_millis: int = 30_000) -> bool:
        return True
