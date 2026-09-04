"""Idempotent OpenTelemetry SDK bootstrap."""

import logging
import os
from collections.abc import Mapping

from opentelemetry import metrics as metrics_api
from opentelemetry import trace as trace_api
from opentelemetry.exporter.otlp.proto.http.metric_exporter import OTLPMetricExporter
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.propagate import set_global_textmap
from opentelemetry.sdk.metrics import MeterProvider as SDKMeterProvider
from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
from opentelemetry.sdk.trace import TracerProvider as SDKTracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.trace.propagation.tracecontext import TraceContextTextMapPropagator

from cortex_otel._context import RequestContextSpanProcessor
from cortex_otel._logging import install_log_record_factory
from cortex_otel._resource import build_resource

_LOG = logging.getLogger("cortex_otel")

_initialised = False
_enabled = False


def _mark_initialised(*, enabled: bool) -> None:
    global _initialised, _enabled
    _initialised = True
    _enabled = enabled


def is_enabled() -> bool:
    """Return whether ``setup_telemetry`` ran with an OTLP endpoint configured."""

    return _enabled


def reset() -> None:
    """Clear module-level init guard. Test-only — do not call from production code."""

    global _initialised, _enabled
    _initialised = False
    _enabled = False


def setup_telemetry(
    *,
    service_name: str,
    service_version: str | None = None,
    emitter_app: str | None = None,
    deployment_environment: str | None = None,
    service_namespace: str | None = None,
    otlp_endpoint: str | None = None,
    extra_resource_attrs: Mapping[str, str] | None = None,
) -> bool:
    """Configure OTel SDK providers, exporters, propagator, and log correlation.

    ``emitter_app`` sets the Resource attribute ``ctx.emitter_app`` (see
    ADR-2026-05-19). Pass the Cortex application name — usually ``"cortex"``
    for platform services, or ``"insight"`` / ``"inalpha"`` when they run
    as first-party emitters.

    Idempotent — repeat calls are no-ops. If neither ``otlp_endpoint`` nor
    ``OTEL_EXPORTER_OTLP_ENDPOINT`` is set, providers are not installed and the
    function returns False. Returns True when the OTLP exporters were wired.
    """

    if _initialised:
        return _enabled

    set_global_textmap(TraceContextTextMapPropagator())

    # Log-record enrichment must run regardless of whether OTLP export is
    # configured: services still emit stdlib logs with structured formatters
    # in local development and CI, and dropping ctx.* there would silently
    # break the labelling contract in exactly the environments engineers
    # look at first.
    install_log_record_factory()

    endpoint = otlp_endpoint or os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
    if not endpoint:
        _LOG.info("cortex_otel.disabled service=%s", service_name)
        _mark_initialised(enabled=False)
        return False

    # OTLPSpanExporter/OTLPMetricExporter only read OTEL_EXPORTER_OTLP_ENDPOINT;
    # they ignore the function parameter. Pass the resolved endpoint explicitly
    # (with the signal-specific path) so the gate and the exporters use the same
    # source of truth.
    base = endpoint.rstrip("/")
    traces_endpoint = f"{base}/v1/traces"
    metrics_endpoint = f"{base}/v1/metrics"

    resource = build_resource(
        service_name=service_name,
        service_version=service_version,
        emitter_app=emitter_app,
        deployment_environment=deployment_environment,
        service_namespace=service_namespace,
        extra=extra_resource_attrs,
    )

    tracer_provider = SDKTracerProvider(resource=resource)
    tracer_provider.add_span_processor(RequestContextSpanProcessor())
    tracer_provider.add_span_processor(
        BatchSpanProcessor(OTLPSpanExporter(endpoint=traces_endpoint))
    )
    trace_api.set_tracer_provider(tracer_provider)

    meter_provider = SDKMeterProvider(
        resource=resource,
        metric_readers=[
            PeriodicExportingMetricReader(OTLPMetricExporter(endpoint=metrics_endpoint))
        ],
    )
    metrics_api.set_meter_provider(meter_provider)

    _LOG.info(
        "cortex_otel.enabled service=%s endpoint=%s",
        service_name,
        endpoint,
    )
    _mark_initialised(enabled=True)
    return True
