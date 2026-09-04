from __future__ import annotations

import json
import logging

from app.main import _LOGRECORD_RESERVED_ATTRS, _StructuredFormatter


def _record(**extra: object) -> logging.LogRecord:
    record = logging.LogRecord(
        name="test.logger",
        level=logging.INFO,
        pathname=__file__,
        lineno=1,
        msg="event",
        args=None,
        exc_info=None,
    )
    for key, value in extra.items():
        setattr(record, key, value)
    return record


def _emit(**extra: object) -> dict:
    return json.loads(_StructuredFormatter().format(_record(**extra)))


def test_baseline_payload_has_timestamp_level_logger_message() -> None:
    payload = _emit()
    assert payload["level"] == "INFO"
    assert payload["logger"] == "test.logger"
    assert payload["message"] == "event"
    assert "timestamp" in payload


def test_extra_fields_are_surfaced_as_top_level_keys() -> None:
    # Regression: admin/app_permissions routes pass tenant_id/role/resource via
    # extra={...}; before the deny-list rewrite these were silently dropped.
    payload = _emit(tenant_id="t1", role="admin", resource="app:x")
    assert payload["tenant_id"] == "t1"
    assert payload["role"] == "admin"
    assert payload["resource"] == "app:x"


def test_ctx_attrs_from_middleware_filter_are_surfaced() -> None:
    payload = _emit(**{"ctx.tenant": "acme", "ctx.app": "cortex", "ctx.request_id": "r-1"})
    assert payload["ctx.tenant"] == "acme"
    assert payload["ctx.app"] == "cortex"
    assert payload["ctx.request_id"] == "r-1"


def test_audit_field_is_surfaced() -> None:
    payload = _emit(audit={"action": "grant", "subject": "u1"})
    assert payload["audit"] == {"action": "grant", "subject": "u1"}


def test_none_values_are_omitted() -> None:
    payload = _emit(tenant_id=None, role="admin")
    assert "tenant_id" not in payload
    assert payload["role"] == "admin"


def test_stdlib_record_attrs_are_not_leaked() -> None:
    payload = _emit()
    for stdlib_attr in ("args", "exc_info", "pathname", "process", "thread", "levelno"):
        assert stdlib_attr not in payload
    assert stdlib_attr in _LOGRECORD_RESERVED_ATTRS


def test_non_json_serialisable_extra_falls_back_to_str() -> None:
    class Opaque:
        def __str__(self) -> str:
            return "opaque-value"

    payload = _emit(obj=Opaque())
    assert payload["obj"] == "opaque-value"
