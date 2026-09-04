from __future__ import annotations

import logging

import pytest
from opentelemetry import metrics as metrics_api
from opentelemetry import trace as trace_api
from opentelemetry.sdk.metrics import MeterProvider as SDKMeterProvider
from opentelemetry.sdk.trace import TracerProvider as SDKTracerProvider

from cortex_otel import setup_telemetry
from cortex_otel._logging import inject_trace_context
from cortex_otel._resource import build_resource


class TestSetupTelemetry:
    def test_no_endpoint_returns_false(self) -> None:
        assert setup_telemetry(service_name="test-svc") is False
        # No SDK provider installed when disabled
        assert not isinstance(trace_api.get_tracer_provider(), SDKTracerProvider)

    def test_endpoint_returns_true_and_installs_sdk(self) -> None:
        assert (
            setup_telemetry(
                service_name="test-svc",
                otlp_endpoint="http://localhost:4318",
            )
            is True
        )
        assert isinstance(trace_api.get_tracer_provider(), SDKTracerProvider)
        assert isinstance(metrics_api.get_meter_provider(), SDKMeterProvider)

    def test_endpoint_from_env(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
        assert setup_telemetry(service_name="test-svc") is True

    def test_idempotent(self) -> None:
        assert (
            setup_telemetry(service_name="test-svc", otlp_endpoint="http://localhost:4318") is True
        )
        first = trace_api.get_tracer_provider()
        assert (
            setup_telemetry(service_name="test-svc", otlp_endpoint="http://localhost:4318") is True
        )
        assert trace_api.get_tracer_provider() is first


class TestBuildResource:
    def test_minimal(self) -> None:
        r = build_resource(service_name="x")
        assert r.attributes["service.name"] == "x"
        assert "service.namespace" not in r.attributes
        assert "ctx.emitter_app" not in r.attributes

    def test_emitter_app(self) -> None:
        r = build_resource(service_name="x", emitter_app="cortex")
        assert r.attributes["ctx.emitter_app"] == "cortex"

    def test_service_namespace_when_explicit(self) -> None:
        r = build_resource(service_name="x", service_namespace="platform")
        assert r.attributes["service.namespace"] == "platform"

    def test_extras_and_version(self) -> None:
        r = build_resource(
            service_name="x",
            service_version="1.2.3",
            extra={"deployment.environment": "local"},
        )
        assert r.attributes["service.version"] == "1.2.3"
        assert r.attributes["deployment.environment"] == "local"


class TestInjectTraceContext:
    def test_injects_when_span_valid(self) -> None:
        tp = SDKTracerProvider()
        tracer = tp.get_tracer("test")
        with tracer.start_as_current_span("op") as span:
            record = logging.LogRecord("n", logging.INFO, "p", 1, "m", None, None)
            inject_trace_context(span, record)
            assert hasattr(record, "trace_id")
            assert hasattr(record, "span_id")
