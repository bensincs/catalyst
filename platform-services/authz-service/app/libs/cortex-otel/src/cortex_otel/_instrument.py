"""Opt-in framework auto-instrumentation helpers.

Each function try-imports its instrumentation package so consumers can pull in
only the extras they need (``cortex-otel[fastapi]``, ``[httpx]``, ``[grpc]``).
When the corresponding extra is not installed, the call is a logged no-op.
"""

import logging
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from fastapi import FastAPI

_LOG = logging.getLogger("cortex_otel")


def instrument_fastapi(app: "FastAPI") -> bool:
    """Auto-instrument a FastAPI application. Returns False if extra missing.

    Installs ``cortex_fastapi_server_request_hook`` as the OTel FastAPI
    ``server_request_hook`` so ``ctx.*`` attributes reach the root request
    span even though ``FastAPIInstrumentor`` wraps ``OpenTelemetryMiddleware``
    outside the user middleware stack. The hook also binds a RequestContext
    on the request's asyncio task so child spans and log records inherit the
    same values via ``current_request_context()``.
    """

    try:
        from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
    except ModuleNotFoundError:
        _LOG.info("cortex_otel.fastapi_skipped reason=extra_not_installed")
        return False

    from cortex_otel._middleware import cortex_fastapi_server_request_hook

    FastAPIInstrumentor.instrument_app(
        app,
        server_request_hook=cortex_fastapi_server_request_hook,
    )
    return True


def instrument_httpx_client(**kwargs: Any) -> bool:
    """Auto-instrument the ``httpx`` client. Returns False if extra missing."""

    try:
        from opentelemetry.instrumentation.httpx import HTTPXClientInstrumentor
    except ModuleNotFoundError:
        _LOG.info("cortex_otel.httpx_skipped reason=extra_not_installed")
        return False

    HTTPXClientInstrumentor().instrument(**kwargs)
    return True


def instrument_grpc_aio_client(**kwargs: Any) -> bool:
    """Auto-instrument outbound async gRPC calls. Returns False if extra missing."""

    try:
        from opentelemetry.instrumentation.grpc import GrpcAioInstrumentorClient
    except ModuleNotFoundError:
        _LOG.info("cortex_otel.grpc_skipped reason=extra_not_installed")
        return False

    GrpcAioInstrumentorClient().instrument(**kwargs)
    return True
