from __future__ import annotations

import logging
from unittest.mock import AsyncMock as _AsyncMock

import pytest
from httpx import AsyncClient

from data.models import CheckPermissionResponse as _CheckResp
from tests.conftest import (
    TENANT_A,
    TENANT_B,
    ZEDTOKEN_READ,
    member_headers,
)

_EXT_AUTHZ_PATH = "/v1/ext-authz/check"


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _capture_audit_records(caplog: pytest.LogCaptureFixture) -> list[dict]:
    records = []
    for record in caplog.records:
        if record.name == "common.audit":
            audit = getattr(record, "audit", None)
            if audit is not None:
                records.append(audit)
    return records


# ---------------------------------------------------------------------------
# AuditEvent unit tests
# ---------------------------------------------------------------------------


class TestAuditEventEmit:
    def test_emit_populates_audit_extra(self, caplog: pytest.LogCaptureFixture) -> None:
        from common.audit import AuditEvent

        with caplog.at_level(logging.INFO, logger="common.audit"):
            AuditEvent(
                tenant_id="t1",
                subject="user:alice",
                action="authz.check",
                resource="agent:x@t1",
                decision="allow",
            ).emit()

        records = _capture_audit_records(caplog)
        assert len(records) == 1
        event = records[0]
        assert event["tenant_id"] == "t1"
        assert event["subject"] == "user:alice"
        assert event["action"] == "authz.check"
        assert event["resource"] == "agent:x@t1"
        assert event["decision"] == "allow"

    def test_emit_includes_timestamp(self, caplog: pytest.LogCaptureFixture) -> None:
        from common.audit import AuditEvent

        with caplog.at_level(logging.INFO, logger="common.audit"):
            AuditEvent(
                tenant_id="t1",
                subject="user:alice",
                action="authz.check",
                resource="agent:x@t1",
                decision="allow",
            ).emit()

        records = _capture_audit_records(caplog)
        assert "timestamp" in records[0]

    def test_emit_includes_latency_when_provided(self, caplog: pytest.LogCaptureFixture) -> None:
        from common.audit import AuditEvent

        with caplog.at_level(logging.INFO, logger="common.audit"):
            AuditEvent(
                tenant_id="t1",
                subject="user:alice",
                action="authz.check",
                resource="agent:x@t1",
                decision="allow",
                latency_ms=42,
            ).emit()

        records = _capture_audit_records(caplog)
        assert records[0]["latency_ms"] == 42

    def test_emit_includes_reason_when_provided(self, caplog: pytest.LogCaptureFixture) -> None:
        from common.audit import AuditEvent

        with caplog.at_level(logging.INFO, logger="common.audit"):
            AuditEvent(
                tenant_id="t1",
                subject="user:alice",
                action="authz.check",
                resource="agent:x@t1",
                decision="deny",
                reason="missing_role",
            ).emit()

        records = _capture_audit_records(caplog)
        assert records[0]["reason"] == "missing_role"

    def test_emit_includes_request_id_when_provided(self, caplog: pytest.LogCaptureFixture) -> None:
        from common.audit import AuditEvent

        with caplog.at_level(logging.INFO, logger="common.audit"):
            AuditEvent(
                tenant_id="t1",
                subject="user:alice",
                action="authz.check",
                resource="agent:x@t1",
                decision="allow",
                request_id="req-xyz-123",
            ).emit()

        records = _capture_audit_records(caplog)
        assert records[0]["request_id"] == "req-xyz-123"

    def test_emit_includes_source_ip_when_provided(self, caplog: pytest.LogCaptureFixture) -> None:
        from common.audit import AuditEvent

        with caplog.at_level(logging.INFO, logger="common.audit"):
            AuditEvent(
                tenant_id="t1",
                subject="user:alice",
                action="authz.check",
                resource="agent:x@t1",
                decision="allow",
                source_ip="10.0.0.1",
            ).emit()

        records = _capture_audit_records(caplog)
        assert records[0]["source_ip"] == "10.0.0.1"

    def test_emit_spreads_metadata_into_audit_record(self, caplog: pytest.LogCaptureFixture) -> None:
        from common.audit import AuditEvent

        with caplog.at_level(logging.INFO, logger="common.audit"):
            AuditEvent(
                tenant_id="t1",
                subject="user:alice",
                action="role.create",
                resource="role:editor",
                decision="success",
                metadata={"role_name": "editor", "parent_role_id": None},
            ).emit()

        records = _capture_audit_records(caplog)
        assert records[0]["role_name"] == "editor"

    # -------------------------------------------------------------------
    # Security invariants — PII-safe subject handling (ADR-26-07-21).
    # These lock in the fail-safe behaviour that keeps raw `sub` / PII
    # out of the audit record. Regressing either of them re-opens the
    # PII leak this PR was written to close.
    # -------------------------------------------------------------------

    def test_emit_omits_subject_when_none(self, caplog: pytest.LogCaptureFixture) -> None:
        """When `subject` is not provided, it MUST NOT appear in the emitted
        payload at all (not as `null`, not as `""`). PII call sites rely on
        omitting the field entirely so cortex-otel's `enduser.id_hash`
        pipeline is the only source of subject identity in the log record.
        """
        from common.audit import AuditEvent

        with caplog.at_level(logging.INFO, logger="common.audit"):
            AuditEvent(
                tenant_id="t1",
                action="authz.check",
                resource="agent:x@t1",
                decision="deny",
            ).emit()

        records = _capture_audit_records(caplog)
        assert len(records) == 1
        assert "subject" not in records[0]

    def test_emit_strips_reserved_keys_from_metadata(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """A caller MUST NOT be able to resurrect or override a reserved
        first-class field (especially `subject`) by threading it through
        `metadata`. Verify every reserved key smuggled in via metadata is
        dropped and the dataclass-declared value (or absence) wins.
        """
        from common.audit import AuditEvent

        with caplog.at_level(logging.INFO, logger="common.audit"):
            AuditEvent(
                tenant_id="t1",
                action="authz.check",
                resource="agent:x@t1",
                decision="deny",
                metadata={
                    # Reserved keys — must all be stripped.
                    "subject": "user:leaked@example.com",
                    "tenant_id": "t-attacker",
                    "action": "authz.override",
                    "resource": "agent:other",
                    "decision": "allow",
                    "reason": "spoofed",
                    "request_id": "spoofed-req",
                    "source_ip": "6.6.6.6",
                    "latency_ms": 999,
                    "timestamp": "1970-01-01T00:00:00+00:00",
                    # Non-reserved key — must be preserved.
                    "role_name": "editor",
                },
            ).emit()

        records = _capture_audit_records(caplog)
        assert len(records) == 1
        event = records[0]

        # Subject must be absent — the metadata smuggle attempt must not
        # resurrect it.
        assert "subject" not in event

        # Dataclass-declared values must win over metadata smuggle attempts.
        assert event["tenant_id"] == "t1"
        assert event["action"] == "authz.check"
        assert event["resource"] == "agent:x@t1"
        assert event["decision"] == "deny"
        assert event.get("reason") is None or event["reason"] != "spoofed"
        assert event.get("request_id") is None or event["request_id"] != "spoofed-req"
        assert event.get("source_ip") is None or event["source_ip"] != "6.6.6.6"
        assert event.get("latency_ms") is None or event["latency_ms"] != 999
        assert event["timestamp"] != "1970-01-01T00:00:00+00:00"

        # Non-reserved metadata still flows through.
        assert event["role_name"] == "editor"

    def test_emit_uses_info_level(self, caplog: pytest.LogCaptureFixture) -> None:
        from common.audit import AuditEvent

        with caplog.at_level(logging.INFO, logger="common.audit"):
            AuditEvent(
                tenant_id="t1",
                subject="user:alice",
                action="authz.check",
                resource="agent:x@t1",
                decision="allow",
            ).emit()

        assert any(r.levelno == logging.INFO for r in caplog.records if r.name == "common.audit")


# ---------------------------------------------------------------------------
# Helper function tests
# ---------------------------------------------------------------------------


class TestAuditHelpers:
    def test_request_id_from_x_request_id_header(self) -> None:
        from common.audit import request_id_from_headers

        result = request_id_from_headers({"x-request-id": "abc-123"})
        assert result == "abc-123"

    def test_request_id_from_x_correlation_id_header(self) -> None:
        from common.audit import request_id_from_headers

        result = request_id_from_headers({"x-correlation-id": "corr-456"})
        assert result == "corr-456"

    def test_request_id_returns_none_when_absent(self) -> None:
        from common.audit import request_id_from_headers

        assert request_id_from_headers({}) is None

    def test_x_request_id_takes_priority_over_correlation_id(self) -> None:
        from common.audit import request_id_from_headers

        result = request_id_from_headers({"x-request-id": "req-1", "x-correlation-id": "corr-1"})
        assert result == "req-1"

    def test_source_ip_from_x_forwarded_for(self) -> None:
        from common.audit import source_ip_from_headers

        result = source_ip_from_headers({"x-forwarded-for": "1.2.3.4, 5.6.7.8"})
        assert result == "1.2.3.4"

    def test_source_ip_from_x_real_ip(self) -> None:
        from common.audit import source_ip_from_headers

        result = source_ip_from_headers({"x-real-ip": "9.8.7.6"})
        assert result == "9.8.7.6"

    def test_source_ip_returns_none_when_absent(self) -> None:
        from common.audit import source_ip_from_headers

        assert source_ip_from_headers({}) is None

    def test_elapsed_ms_returns_non_negative_int(self) -> None:
        import time
        from common.audit import elapsed_ms

        start = time.monotonic()
        result = elapsed_ms(start)
        assert isinstance(result, int)
        assert result >= 0


class TestSubjectId:
    """`subject_id` produces the salted-sha256 pseudonym that matches the
    OTel agent's identity-hash stage (ADR-26-07-21). When the salt env var
    is unset we MUST return None so callers omit the pseudonym rather than
    emit a rainbow-tableable value."""

    def test_returns_none_when_salt_env_unset(self, monkeypatch: pytest.MonkeyPatch) -> None:
        from common.audit import subject_id

        monkeypatch.delenv("CORTEX_REDACTION_HASH_SALT", raising=False)
        assert subject_id("alice@example.com") is None

    def test_returns_none_when_salt_env_empty(self, monkeypatch: pytest.MonkeyPatch) -> None:
        from common.audit import subject_id

        monkeypatch.setenv("CORTEX_REDACTION_HASH_SALT", "")
        assert subject_id("alice@example.com") is None

    def test_returns_none_when_subject_empty(self, monkeypatch: pytest.MonkeyPatch) -> None:
        from common.audit import subject_id

        monkeypatch.setenv("CORTEX_REDACTION_HASH_SALT", "test-salt")
        assert subject_id(None) is None
        assert subject_id("") is None

    def test_returns_deterministic_16_char_hex_when_salt_set(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        import hashlib

        from common.audit import subject_id

        monkeypatch.setenv("CORTEX_REDACTION_HASH_SALT", "test-salt")
        sub = "alice@example.com"
        expected_unsalted = hashlib.sha256(sub.encode()).hexdigest()
        expected = hashlib.sha256(("test-salt" + expected_unsalted).encode()).hexdigest()[:16]

        result = subject_id(sub)
        assert result == expected
        assert len(result) == 16
        assert all(c in "0123456789abcdef" for c in result)

    def test_different_salts_produce_different_ids(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        from common.audit import subject_id

        monkeypatch.setenv("CORTEX_REDACTION_HASH_SALT", "salt-a")
        first = subject_id("alice@example.com")

        monkeypatch.setenv("CORTEX_REDACTION_HASH_SALT", "salt-b")
        second = subject_id("alice@example.com")

        assert first != second


# ---------------------------------------------------------------------------
# ext_authz_router audit integration
# ---------------------------------------------------------------------------


class TestExtAuthzAuditLogging:
    @pytest.mark.asyncio
    async def test_allowed_request_emits_allow_event(
        self, client: AsyncClient, caplog: pytest.LogCaptureFixture
    ) -> None:
        with caplog.at_level(logging.INFO, logger="common.audit"):
            await client.post(
                _EXT_AUTHZ_PATH,
                headers={**member_headers(tenant_id=TENANT_A), "host": f"cortex-ui.{TENANT_A}.cortex.ai"},
            )

        records = _capture_audit_records(caplog)
        assert any(r["decision"] == "allow" and r["action"] == "ext_authz.check" for r in records)

    @pytest.mark.asyncio
    async def test_allowed_event_contains_tenant_and_subject(
        self, client: AsyncClient, caplog: pytest.LogCaptureFixture
    ) -> None:
        with caplog.at_level(logging.INFO, logger="common.audit"):
            await client.post(
                _EXT_AUTHZ_PATH,
                headers={**member_headers(tenant_id=TENANT_A), "host": f"cortex-ui.{TENANT_A}.cortex.ai"},
            )

        records = _capture_audit_records(caplog)
        event = next(r for r in records if r["action"] == "ext_authz.check")
        assert event["tenant_id"] == TENANT_A

    @pytest.mark.asyncio
    async def test_unauthenticated_request_emits_deny_event(
        self, client: AsyncClient, caplog: pytest.LogCaptureFixture
    ) -> None:
        with caplog.at_level(logging.INFO, logger="common.audit"):
            await client.post(
                _EXT_AUTHZ_PATH,
                headers={"host": f"cortex-ui.{TENANT_A}.cortex.ai"},
            )

        records = _capture_audit_records(caplog)
        assert any(r["decision"] == "deny" and r["action"] == "ext_authz.check" for r in records)

    @pytest.mark.asyncio
    async def test_cross_tenant_request_emits_deny_event(
        self, client: AsyncClient, caplog: pytest.LogCaptureFixture,
        mock_spicedb_client: _AsyncMock,
    ) -> None:
        mock_spicedb_client.check_permission = _AsyncMock(
            return_value=_CheckResp(allowed=False, checked_at=ZEDTOKEN_READ)
        )
        with caplog.at_level(logging.INFO, logger="common.audit"):
            await client.post(
                _EXT_AUTHZ_PATH,
                headers={
                    **member_headers(tenant_id=TENANT_A),
                    "host": f"cortex-ui.{TENANT_B}.cortex.ai",
                },
            )

        records = _capture_audit_records(caplog)
        deny_events = [r for r in records if r["decision"] == "deny" and r["action"] == "ext_authz.check"]
        assert len(deny_events) == 1
        assert "spicedb_denied" in deny_events[0]["reason"]

    @pytest.mark.asyncio
    async def test_health_path_emits_health_bypass_event(
        self, client: AsyncClient, caplog: pytest.LogCaptureFixture
    ) -> None:
        with caplog.at_level(logging.INFO, logger="common.audit"):
            await client.get(f"{_EXT_AUTHZ_PATH}/health")

        records = _capture_audit_records(caplog)
        assert any(r["reason"] == "health_bypass" for r in records)

    @pytest.mark.asyncio
    async def test_audit_event_contains_latency_ms(
        self, client: AsyncClient, caplog: pytest.LogCaptureFixture
    ) -> None:
        with caplog.at_level(logging.INFO, logger="common.audit"):
            await client.post(
                _EXT_AUTHZ_PATH,
                headers={**member_headers(tenant_id=TENANT_A), "host": f"cortex-ui.{TENANT_A}.cortex.ai"},
            )

        records = _capture_audit_records(caplog)
        event = next(r for r in records if r["action"] == "ext_authz.check")
        assert "latency_ms" in event
        assert isinstance(event["latency_ms"], int)

    @pytest.mark.asyncio
    async def test_request_id_header_propagated_to_audit_event(
        self, client: AsyncClient, caplog: pytest.LogCaptureFixture
    ) -> None:
        headers = {
            **member_headers(tenant_id=TENANT_A),
            "host": f"cortex-ui.{TENANT_A}.cortex.ai",
            "x-request-id": "test-req-42",
        }
        with caplog.at_level(logging.INFO, logger="common.audit"):
            await client.post(_EXT_AUTHZ_PATH, headers=headers)

        records = _capture_audit_records(caplog)
        event = next(r for r in records if r["action"] == "ext_authz.check")
        assert event["request_id"] == "test-req-42"

# ---------------------------------------------------------------------------
# TestRoleRouterAuditLogging, TestPermissionRouterAuditLogging, and
# TestRoutesAuditLogging have been removed.
#
# The routes they tested (/v1/roles, /v1/permissions/*, /v1/permissions/check,
# /v1/permissions/grant, /v1/permissions/revoke) were deleted as part of the
# authz simplification (single can_access permission, no role/permission mgmt
# API surface). Only ext_authz and health routes remain.
# ---------------------------------------------------------------------------
