"""Regression: RequestContextASGIMiddleware must sit outside instrument_fastapi.

Starlette's ``add_middleware`` prepends, so the last-added middleware ends up
outermost. If we install the OTel FastAPI instrumentation *after*
RequestContextASGIMiddleware, OpenTelemetryMiddleware becomes outer and the
root request span starts *before* the ctx.* ContextVar is bound — meaning
``RequestContextSpanProcessor.on_start`` fires with an empty context and the
span exports without ``ctx.tenant``, ``ctx.app`` etc.

These tests import ``build_app`` and hit it through a real ASGI transport,
capturing exported spans in-memory, so any regression in the middleware
ordering surfaces here before it reaches an environment.
"""

from __future__ import annotations

import pytest
from httpx import ASGITransport, AsyncClient
from opentelemetry import trace as trace_api
from opentelemetry.sdk.trace import TracerProvider as SDKTracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter

from cortex_otel._context import RequestContextSpanProcessor


@pytest.fixture
def in_memory_tracer() -> InMemorySpanExporter:
    """Install an in-memory SDK TracerProvider on the OTel global for this test.

    setup_telemetry runs at authz-service import time and, without an OTLP
    endpoint, leaves the global provider as the no-op ProxyTracerProvider —
    which drops spans. Swap in a real SDK provider with the ctx.* processor
    plus an in-memory exporter so we can assert on the recorded spans.
    """
    exporter = InMemorySpanExporter()
    provider = SDKTracerProvider()
    provider.add_span_processor(RequestContextSpanProcessor())
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    trace_api._TRACER_PROVIDER_SET_ONCE._done = False  # type: ignore[attr-defined]
    trace_api.set_tracer_provider(provider)
    return exporter


@pytest.mark.asyncio
async def test_root_http_span_carries_ctx_attributes(
    in_memory_tracer: InMemorySpanExporter,
) -> None:
    from app.main import build_app  # imported lazily so setup_telemetry ran first

    app = build_app()
    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        resp = await client.get(
            "/health",
            headers={
                "X-Ctx-Tenant": "acme",
                "X-Ctx-App": "assistant",
                "X-Request-Id": "req-abc",
            },
        )
    assert resp.status_code == 200

    spans = in_memory_tracer.get_finished_spans()
    assert spans, "expected at least one exported span for the request"

    # The FastAPI-instrumented root span for the request must have ctx.*
    # stamped by RequestContextSpanProcessor. If the middleware order regresses
    # (OTel outer, RequestContext inner) the root span is created before the
    # ctx var is bound and this assertion fails.
    http_spans = [s for s in spans if s.attributes and "http.method" in s.attributes]
    assert http_spans, "expected at least one span with http.method attribute"
    root = http_spans[0]
    assert root.attributes["ctx.tenant"] == "acme"
    assert root.attributes["ctx.app"] == "assistant"
    assert root.attributes["ctx.request_id"] == "req-abc"
