from __future__ import annotations

import pytest

import cortex_otel._setup as setup_module


@pytest.fixture(autouse=True)
def _reset_otel_state(monkeypatch: pytest.MonkeyPatch) -> None:
    """Reset module-level init guard + global SDK providers between tests.

    The OTel SDK is process-global, so each test must start from a clean slate.
    """

    from opentelemetry import metrics as metrics_api
    from opentelemetry import trace as trace_api

    monkeypatch.delenv("OTEL_EXPORTER_OTLP_ENDPOINT", raising=False)
    setup_module.reset()

    yield

    setup_module.reset()
    trace_api._TRACER_PROVIDER = None  # type: ignore[attr-defined]
    metrics_api._METER_PROVIDER = None  # type: ignore[attr-defined]
