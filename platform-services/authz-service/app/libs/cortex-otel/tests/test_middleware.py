from __future__ import annotations

import base64
import hashlib
import json
from typing import Any

import pytest

from cortex_otel import RequestContextASGIMiddleware, current_request_context


def _make_jwt(payload: dict[str, Any]) -> bytes:
    """Build a minimal unsigned JWT for tests — signature is a fixed dummy string."""

    def _b64(data: bytes) -> str:
        return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")

    header = _b64(b'{"alg":"HS256","typ":"JWT"}')
    body = _b64(json.dumps(payload).encode("utf-8"))
    signature = _b64(b"not-verified")
    return f"Bearer {header}.{body}.{signature}".encode("ascii")


async def _run(mw: RequestContextASGIMiddleware, headers: list[tuple[bytes, bytes]]) -> Any:
    captured: dict[str, Any] = {}

    async def _receive() -> dict[str, Any]:
        return {"type": "http.request", "body": b"", "more_body": False}

    async def _send(_msg: dict[str, Any]) -> None:
        return

    scope = {
        "type": "http",
        "method": "GET",
        "path": "/",
        "headers": headers,
    }
    await mw(scope, _receive, _send)
    return captured


@pytest.mark.asyncio
async def test_binds_ctx_from_headers() -> None:
    seen: dict[str, str | None] = {}

    async def app(scope: dict[str, Any], receive: Any, send: Any) -> None:
        ctx = current_request_context()
        assert ctx is not None
        seen["tenant"] = ctx.tenant
        seen["app"] = ctx.app
        seen["request_id"] = ctx.request_id
        seen["workflow_id"] = ctx.workflow_id
        seen["agent_run_id"] = ctx.agent_run_id

    mw = RequestContextASGIMiddleware(app)
    await _run(
        mw,
        [
            (b"x-request-id", b"req-123"),
            (b"x-ctx-tenant", b"acme"),
            (b"x-ctx-app", b"assistant"),
            (b"x-ctx-workflow-id", b"w1"),
            (b"x-ctx-agent-run-id", b"ar1"),
        ],
    )
    assert seen == {
        "tenant": "acme",
        "app": "assistant",
        "request_id": "req-123",
        "workflow_id": "w1",
        "agent_run_id": "ar1",
    }


@pytest.mark.asyncio
async def test_generates_request_id_when_missing() -> None:
    seen: dict[str, str | None] = {}

    async def app(scope: dict[str, Any], receive: Any, send: Any) -> None:
        ctx = current_request_context()
        assert ctx is not None
        seen["request_id"] = ctx.request_id

    mw = RequestContextASGIMiddleware(app)
    await _run(mw, [])
    assert seen["request_id"] is not None
    # Canonical dashed uuid4 form so python and go generate byte-for-byte
    # identical shapes when correlating a request across a language hop.
    import uuid as _uuid

    parsed = _uuid.UUID(seen["request_id"])
    assert str(parsed) == seen["request_id"]
    assert parsed.version == 4


@pytest.mark.asyncio
async def test_context_cleared_after_request() -> None:
    async def app(scope: dict[str, Any], receive: Any, send: Any) -> None:
        assert current_request_context() is not None

    mw = RequestContextASGIMiddleware(app)
    await _run(mw, [(b"x-ctx-tenant", b"acme")])
    assert current_request_context() is None


@pytest.mark.asyncio
async def test_lifespan_passthrough() -> None:
    called = False

    async def app(scope: dict[str, Any], receive: Any, send: Any) -> None:
        nonlocal called
        called = True
        assert current_request_context() is None

    mw = RequestContextASGIMiddleware(app)
    scope = {"type": "lifespan"}

    async def _receive() -> dict[str, Any]:
        return {"type": "lifespan.startup"}

    async def _send(_msg: dict[str, Any]) -> None:
        return

    await mw(scope, _receive, _send)
    assert called is True


@pytest.mark.asyncio
async def test_enduser_id_hash_from_jwt_sub() -> None:
    seen: dict[str, str | None] = {}

    async def app(scope: dict[str, Any], receive: Any, send: Any) -> None:
        ctx = current_request_context()
        assert ctx is not None
        seen["enduser_id_hash"] = ctx.enduser_id_hash

    mw = RequestContextASGIMiddleware(app)
    await _run(mw, [(b"authorization", _make_jwt({"sub": "user-42"}))])
    expected = hashlib.sha256(b"user-42").hexdigest()
    assert seen["enduser_id_hash"] == expected


@pytest.mark.asyncio
async def test_enduser_id_hash_falls_back_to_oid() -> None:
    seen: dict[str, str | None] = {}

    async def app(scope: dict[str, Any], receive: Any, send: Any) -> None:
        ctx = current_request_context()
        assert ctx is not None
        seen["enduser_id_hash"] = ctx.enduser_id_hash

    mw = RequestContextASGIMiddleware(app)
    await _run(mw, [(b"authorization", _make_jwt({"oid": "azure-oid-1"}))])
    assert seen["enduser_id_hash"] == hashlib.sha256(b"azure-oid-1").hexdigest()


@pytest.mark.asyncio
async def test_enduser_id_hash_none_without_authorization() -> None:
    seen: dict[str, str | None] = {}

    async def app(scope: dict[str, Any], receive: Any, send: Any) -> None:
        ctx = current_request_context()
        assert ctx is not None
        seen["enduser_id_hash"] = ctx.enduser_id_hash

    mw = RequestContextASGIMiddleware(app)
    await _run(mw, [])
    assert seen["enduser_id_hash"] is None


@pytest.mark.asyncio
async def test_enduser_id_hash_none_on_oversized_token() -> None:
    seen: dict[str, str | None] = {}

    async def app(scope: dict[str, Any], receive: Any, send: Any) -> None:
        ctx = current_request_context()
        assert ctx is not None
        seen["enduser_id_hash"] = ctx.enduser_id_hash

    mw = RequestContextASGIMiddleware(app)
    big = b"Bearer " + b"a" * (16 * 1024)
    await _run(mw, [(b"authorization", big)])
    assert seen["enduser_id_hash"] is None


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "authorization",
    [
        b"Basic dXNlcjpwYXNz",
        b"Bearer not-a-jwt",
        b"Bearer aGVsbG8=.bm90LWpzb24=.sig",
        b"Bearer " + base64.urlsafe_b64encode(b'{"alg":"HS256"}').rstrip(b"=") + b".e30.sig",
    ],
    ids=["not-bearer", "single-segment", "non-json-payload", "empty-payload"],
)
async def test_enduser_id_hash_none_on_malformed(authorization: bytes) -> None:
    seen: dict[str, str | None] = {}

    async def app(scope: dict[str, Any], receive: Any, send: Any) -> None:
        ctx = current_request_context()
        assert ctx is not None
        seen["enduser_id_hash"] = ctx.enduser_id_hash

    mw = RequestContextASGIMiddleware(app)
    await _run(mw, [(b"authorization", authorization)])
    assert seen["enduser_id_hash"] is None


@pytest.mark.asyncio
async def test_trusted_x_cortex_tenant_overrides_client_x_ctx_tenant() -> None:
    """Gateway-injected x-cortex-tenant wins over client-supplied x-ctx-tenant."""

    seen: dict[str, str | None] = {}

    async def app(scope: dict[str, Any], receive: Any, send: Any) -> None:
        ctx = current_request_context()
        assert ctx is not None
        seen["tenant"] = ctx.tenant

    mw = RequestContextASGIMiddleware(app)
    await _run(
        mw,
        [
            (b"x-ctx-tenant", b"attacker-supplied"),
            (b"x-cortex-tenant", b"gateway-issued"),
        ],
    )
    assert seen["tenant"] == "gateway-issued"


@pytest.mark.asyncio
async def test_trusted_x_cortex_app_overrides_client_x_ctx_app() -> None:
    seen: dict[str, str | None] = {}

    async def app(scope: dict[str, Any], receive: Any, send: Any) -> None:
        ctx = current_request_context()
        assert ctx is not None
        seen["app"] = ctx.app

    mw = RequestContextASGIMiddleware(app)
    await _run(
        mw,
        [
            (b"x-ctx-app", b"attacker-supplied"),
            (b"x-cortex-app", b"gateway-issued"),
        ],
    )
    assert seen["app"] == "gateway-issued"


@pytest.mark.asyncio
async def test_x_cortex_sub_used_for_enduser_hash_instead_of_authorization() -> None:
    """Prefer the pre-validated gateway subject over parsing the JWT ourselves."""

    seen: dict[str, str | None] = {}

    async def app(scope: dict[str, Any], receive: Any, send: Any) -> None:
        ctx = current_request_context()
        assert ctx is not None
        seen["enduser_id_hash"] = ctx.enduser_id_hash

    mw = RequestContextASGIMiddleware(app)
    await _run(
        mw,
        [
            (b"authorization", _make_jwt({"sub": "unverified"})),
            (b"x-cortex-sub", b"gateway-verified"),
        ],
    )
    assert seen["enduser_id_hash"] == hashlib.sha256(b"gateway-verified").hexdigest()


@pytest.mark.asyncio
async def test_x_ctx_tenant_still_used_when_no_x_cortex_tenant() -> None:
    """Intra-mesh service-to-service propagation via X-Ctx-* still works."""

    seen: dict[str, str | None] = {}

    async def app(scope: dict[str, Any], receive: Any, send: Any) -> None:
        ctx = current_request_context()
        assert ctx is not None
        seen["tenant"] = ctx.tenant
        seen["app"] = ctx.app

    mw = RequestContextASGIMiddleware(app)
    await _run(
        mw,
        [
            (b"x-ctx-tenant", b"acme"),
            (b"x-ctx-app", b"assistant"),
        ],
    )
    assert seen == {"tenant": "acme", "app": "assistant"}
