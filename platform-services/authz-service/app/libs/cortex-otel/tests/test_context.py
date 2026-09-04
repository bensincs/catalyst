from __future__ import annotations

import logging

from opentelemetry.sdk.trace import TracerProvider as SDKTracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter

from cortex_otel import (
    RequestContext,
    current_request_context,
    request_context,
)
from cortex_otel._context import (
    RequestContextSpanProcessor,
    context_to_attributes,
)
from cortex_otel._logging import (
    _reset_log_record_factory_for_tests,
    inject_trace_context,
    install_log_record_factory,
)


class TestRequestContextManager:
    def test_no_context_by_default(self) -> None:
        assert current_request_context() is None

    def test_bind_and_unbind(self) -> None:
        with request_context(tenant="acme", app="assistant") as ctx:
            assert current_request_context() == ctx
            assert ctx.tenant == "acme"
            assert ctx.app == "assistant"
        assert current_request_context() is None

    def test_nested_inherits_missing_fields(self) -> None:
        with request_context(tenant="acme", app="assistant", request_id="r1"):
            with request_context(workflow_id="w1") as inner:
                assert inner.tenant == "acme"
                assert inner.app == "assistant"
                assert inner.request_id == "r1"
                assert inner.workflow_id == "w1"

    def test_nested_overrides_explicit(self) -> None:
        with request_context(tenant="acme"):
            with request_context(tenant="widget") as inner:
                assert inner.tenant == "widget"


class TestContextToAttributes:
    def test_empty_context_produces_no_attrs(self) -> None:
        assert context_to_attributes(RequestContext()) == {}

    def test_populated_context_maps_to_adr_keys(self) -> None:
        ctx = RequestContext(
            app="assistant",
            tenant="acme",
            request_id="r1",
            agent_run_id="ar1",
            workflow_id="w1",
            enduser_id_hash="hash-abc",
        )
        assert context_to_attributes(ctx) == {
            "ctx.app": "assistant",
            "ctx.tenant": "acme",
            "ctx.request_id": "r1",
            "ctx.agent_run_id": "ar1",
            "ctx.workflow_id": "w1",
            "enduser.id_hash": "hash-abc",
        }


class TestRequestContextSpanProcessor:
    def _tracer_and_exporter(self) -> tuple[SDKTracerProvider, InMemorySpanExporter]:
        exporter = InMemorySpanExporter()
        provider = SDKTracerProvider()
        provider.add_span_processor(RequestContextSpanProcessor())
        provider.add_span_processor(SimpleSpanProcessor(exporter))
        return provider, exporter

    def test_span_gets_stamped_inside_request_context(self) -> None:
        provider, exporter = self._tracer_and_exporter()
        tracer = provider.get_tracer("test")
        with request_context(tenant="acme", app="assistant", request_id="r1"):
            with tracer.start_as_current_span("op"):
                pass
        (span,) = exporter.get_finished_spans()
        assert span.attributes["ctx.tenant"] == "acme"
        assert span.attributes["ctx.app"] == "assistant"
        assert span.attributes["ctx.request_id"] == "r1"

    def test_span_outside_context_is_not_stamped(self) -> None:
        provider, exporter = self._tracer_and_exporter()
        tracer = provider.get_tracer("test")
        with tracer.start_as_current_span("op"):
            pass
        (span,) = exporter.get_finished_spans()
        assert "ctx.tenant" not in (span.attributes or {})


class TestLogHookStampsContext:
    def test_ctx_attrs_added_to_log_record(self) -> None:
        provider = SDKTracerProvider()
        tracer = provider.get_tracer("test")
        with tracer.start_as_current_span("op") as span:
            record = logging.LogRecord("n", logging.INFO, "p", 1, "m", None, None)
            with request_context(tenant="acme", request_id="r1"):
                inject_trace_context(span, record)
        assert getattr(record, "ctx.tenant") == "acme"
        assert getattr(record, "ctx.request_id") == "r1"
        assert hasattr(record, "trace_id")

    def test_no_ctx_attrs_when_no_context(self) -> None:
        provider = SDKTracerProvider()
        tracer = provider.get_tracer("test")
        with tracer.start_as_current_span("op") as span:
            record = logging.LogRecord("n", logging.INFO, "p", 1, "m", None, None)
            inject_trace_context(span, record)
        assert not hasattr(record, "ctx.tenant")


class TestLogRecordFactory:
    """Regression: the labelling contract must fire on every stdlib log record,
    including those emitted outside any OTel span, so ``ctx.*`` shows up in
    local runs where no OTLP endpoint is configured."""

    def test_factory_stamps_ctx_on_records_outside_a_span(self) -> None:
        install_log_record_factory()
        try:
            with request_context(tenant="acme", request_id="r1"):
                record = logging.getLogger("t").makeRecord(
                    "t", logging.INFO, "p", 1, "m", None, None
                )
            assert getattr(record, "ctx.tenant") == "acme"
            assert getattr(record, "ctx.request_id") == "r1"
        finally:
            _reset_log_record_factory_for_tests()

    def test_factory_is_idempotent(self) -> None:
        install_log_record_factory()
        install_log_record_factory()
        try:
            with request_context(tenant="acme"):
                record = logging.getLogger("t").makeRecord(
                    "t", logging.INFO, "p", 1, "m", None, None
                )
            assert getattr(record, "ctx.tenant") == "acme"
            # If we'd wrapped twice, the previously captured factory would
            # itself be our wrapper and reset would only unwind one layer.
        finally:
            _reset_log_record_factory_for_tests()
        # After reset the plain LogRecord factory must not add ctx.*.
        with request_context(tenant="acme"):
            record = logging.getLogger("t").makeRecord("t", logging.INFO, "p", 1, "m", None, None)
        assert not hasattr(record, "ctx.tenant")

    def test_factory_no_op_when_no_context(self) -> None:
        install_log_record_factory()
        try:
            record = logging.getLogger("t").makeRecord("t", logging.INFO, "p", 1, "m", None, None)
        finally:
            _reset_log_record_factory_for_tests()
        assert not hasattr(record, "ctx.tenant")
