"""Shared OpenTelemetry SDK bootstrap for Cortex Python services."""

from cortex_otel import _attributes as attributes
from cortex_otel._context import (
    RequestContext,
    current_request_context,
    request_context,
)
from cortex_otel._instrument import (
    instrument_fastapi,
    instrument_grpc_aio_client,
    instrument_httpx_client,
)
from cortex_otel._middleware import RequestContextASGIMiddleware
from cortex_otel._resource import build_resource
from cortex_otel._setup import setup_telemetry

__all__ = [
    "RequestContext",
    "RequestContextASGIMiddleware",
    "attributes",
    "build_resource",
    "current_request_context",
    "instrument_fastapi",
    "instrument_grpc_aio_client",
    "instrument_httpx_client",
    "request_context",
    "setup_telemetry",
]
