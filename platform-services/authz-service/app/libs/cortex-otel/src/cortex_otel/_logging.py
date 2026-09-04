"""Log-record enrichment with active trace context and request context."""

from __future__ import annotations

import logging
from collections.abc import Callable
from typing import Any

from opentelemetry.trace import Span, format_span_id, format_trace_id, get_current_span

from cortex_otel._context import context_to_attributes, current_request_context


def inject_trace_context(span: Span, record: logging.LogRecord) -> None:
    """Enrich a stdlib LogRecord with trace context and Cortex request attributes.

    Attaches ``trace_id`` / ``span_id`` when a valid span is active, and
    stamps every ``ctx.*`` / ``enduser.id_hash`` value bound via
    ``request_context(...)`` onto the record. Both are safe no-ops when
    nothing is bound.
    """

    span_context = span.get_span_context()
    if span_context.is_valid:
        record.trace_id = format_trace_id(span_context.trace_id)
        record.span_id = format_span_id(span_context.span_id)

    ctx = current_request_context()
    if ctx is None:
        return
    for key, value in context_to_attributes(ctx).items():
        setattr(record, key, value)


_previous_factory: Callable[..., logging.LogRecord] | None = None


def install_log_record_factory() -> None:
    """Install a LogRecord factory that stamps ctx.* + trace_id/span_id on every record.

    Fires on every stdlib log record — inside or outside a span, in tests or
    production — without depending on the OTel LoggingInstrumentor path (which
    only calls its hook when a valid span is active). Idempotent; a repeat
    call reuses the previously captured underlying factory.
    """

    global _previous_factory
    if _previous_factory is not None:
        return
    _previous_factory = logging.getLogRecordFactory()

    def _factory(*args: Any, **kwargs: Any) -> logging.LogRecord:
        record = _previous_factory(*args, **kwargs)  # type: ignore[misc]
        inject_trace_context(get_current_span(), record)
        return record

    logging.setLogRecordFactory(_factory)


def _reset_log_record_factory_for_tests() -> None:
    """Restore the LogRecord factory captured by ``install_log_record_factory``.

    Test-only helper. Leading underscore + explicit suffix because it will
    unconditionally replace whatever factory is currently installed; if a
    third-party library installed its own factory *after* us, this would
    clobber it. Safe when nothing was installed.
    """

    global _previous_factory
    if _previous_factory is None:
        return
    logging.setLogRecordFactory(_previous_factory)
    _previous_factory = None
