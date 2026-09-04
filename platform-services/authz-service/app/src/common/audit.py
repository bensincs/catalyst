from __future__ import annotations

import hashlib
import logging
import os
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

_logger = logging.getLogger("common.audit")

# Length of the hex prefix returned by `subject_id`. 16 hex chars = 64 bits of
# collision resistance — enough for human disambiguation while keeping the
# deny-response body short. A Log Analytics prefix match still correlates
# (`where enduser_id_hash startswith "<value>"`).
_SUBJECT_ID_LEN = 16


def subject_id(sub: str | None) -> str | None:
    """Salted-sha256 pseudonym for a subject, matching the OTel agent's
    redaction stage.

    Returns `None` when the raw subject is empty OR when the shared
    `CORTEX_REDACTION_HASH_SALT` env var is unset — callers MUST omit the
    identifier from user-facing text in that case (fail-safe). The value we
    emit is `sha256(salt + sha256(sub).hexdigest())[:16]` — i.e. the salt is
    concatenated with the *hex* form of the inner sha256, not its raw bytes,
    matching the OTel agent's `enduser.id_hash` derivation per ADR-26-07-21
    so operators can correlate a deny response back to its log record.
    """
    if not sub:
        return None
    salt = os.environ.get("CORTEX_REDACTION_HASH_SALT", "")
    if not salt:
        return None
    unsalted = hashlib.sha256(sub.encode("utf-8")).hexdigest()
    return hashlib.sha256((salt + unsalted).encode("utf-8")).hexdigest()[:_SUBJECT_ID_LEN]


@dataclass
class AuditEvent:
    tenant_id: str
    # `subject` carries the raw JWT `sub` / email in call sites where the
    # value is meaningful (e.g. "bootstrap", "anonymous", "probe"). PII-
    # bearing call sites MUST omit it — cortex-otel stamps an *unsalted*
    # `enduser.id_hash` on every log record via its LogRecord factory, and
    # the OTel agent's `transform/hash-identity` stage re-hashes it with the
    # cluster salt so the value in Log Analytics is not rainbow-tableable.
    # See ADR-26-07-21.
    action: str
    resource: str
    decision: str
    subject: str | None = None
    reason: str | None = None
    request_id: str | None = None
    source_ip: str | None = None
    latency_ms: int | None = None
    metadata: dict[str, Any] = field(default_factory=dict)
    timestamp: str = field(default_factory=lambda: datetime.now(timezone.utc).isoformat())

    def emit(self) -> None:
        # Strip reserved top-level keys from metadata so a caller cannot
        # shadow (or resurrect) `subject` — or any other first-class field —
        # by threading it through `metadata`. This closes the "subject was
        # omitted from the dataclass but re-appeared via metadata" leak.
        reserved = {
            "timestamp",
            "tenant_id",
            "action",
            "resource",
            "decision",
            "reason",
            "request_id",
            "source_ip",
            "latency_ms",
            "subject",
        }
        metadata_safe = {k: v for k, v in self.metadata.items() if k not in reserved}
        payload: dict[str, Any] = {
            "timestamp": self.timestamp,
            "tenant_id": self.tenant_id,
            "action": self.action,
            "resource": self.resource,
            "decision": self.decision,
            "reason": self.reason,
            "request_id": self.request_id,
            "source_ip": self.source_ip,
            "latency_ms": self.latency_ms,
            **metadata_safe,
        }
        if self.subject is not None:
            payload["subject"] = self.subject
        _logger.info("authz_audit", extra={"audit": payload})


def request_id_from_headers(headers: dict[str, str]) -> str | None:
    return headers.get("x-request-id") or headers.get("x-correlation-id")


def source_ip_from_headers(headers: dict[str, str]) -> str | None:
    forwarded_for = headers.get("x-forwarded-for")
    if forwarded_for:
        return forwarded_for.split(",")[0].strip()
    return headers.get("x-real-ip")


def elapsed_ms(start: float) -> int:
    return int((time.monotonic() - start) * 1000)


@dataclass
class RequestContext:
    start: float
    req_id: str | None
    src_ip: str | None
    raw_headers: dict[str, str]


def request_context_from(request) -> RequestContext:
    raw_headers = dict(request.headers)
    return RequestContext(
        start=time.monotonic(),
        req_id=request_id_from_headers(raw_headers),
        src_ip=source_ip_from_headers(raw_headers),
        raw_headers=raw_headers,
    )
