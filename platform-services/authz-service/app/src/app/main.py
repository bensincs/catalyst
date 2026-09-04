from __future__ import annotations

import json
import logging
import os
from contextlib import asynccontextmanager
from typing import AsyncIterator

from cortex_otel import (
    instrument_fastapi,
    instrument_grpc_aio_client,
    setup_telemetry,
)
from fastapi import FastAPI, Request
from slowapi import Limiter, _rate_limit_exceeded_handler
from slowapi.errors import RateLimitExceeded

from routes.health import router as health_router
from routes.ext_authz import router as ext_authz_router
from routes.admin import router as admin_router
from routes.app_permissions import router as app_permissions_router

# Bootstrap the OTel SDK once per process, before any FastAPI/grpc instrumentation
# runs. cortex-otel is idempotent so build_app() re-entry under uvicorn --factory
# is safe, but the global propagator and providers are best set deterministically
# at import time.
#
# emitter_app values are lowercase and defined by ADR-2026-05-19:
#   - "cortex"  — platform services (authz, tenant-operator, workflow-*, ai-gateway)
#   - "insight" — the InSight application, when it runs as a first-party emitter
#   - "inalpha" — the InAlpha application, when it runs as a first-party emitter
# authz-service is a platform service, so it emits as "cortex".
setup_telemetry(
    service_name=os.getenv("OTEL_SERVICE_NAME", "authz-service"),
    emitter_app="cortex",
)
instrument_grpc_aio_client()


def _get_tenant_id(request: Request) -> str:
    tenant_id = request.headers.get("x-cortex-tenant", "unknown")
    return f"tenant:{tenant_id}"


limiter = Limiter(
    key_func=_get_tenant_id,
    default_limits=["100/minute", "2000/hour"],
)


# Attributes stdlib logging always puts on a LogRecord. Anything *else* on
# record.__dict__ came from either extra={...} at the call site or from a
# logging filter (e.g. RequestContextLoggingFilter, which stamps ctx.*), and is
# what we actually want to surface as structured JSON. Built once by inspecting
# a synthetic record so we track stdlib evolution rather than hand-listing.
_LOGRECORD_RESERVED_ATTRS = frozenset(
    logging.LogRecord("", 0, "", 0, "", None, None).__dict__
) | {"message", "asctime"}


class _StructuredFormatter(logging.Formatter):
    def format(self, record: logging.LogRecord) -> str:
        payload: dict = {
            "timestamp": self.formatTime(record),
            "level": record.levelname,
            "logger": record.name,
            "message": record.getMessage(),
        }
        # Surface every non-stdlib attribute — ctx.* from the OTel middleware
        # filter, `audit` from AuditEvent.emit(), and any extra={...} bag
        # passed at the call site (e.g. admin/app_permissions mutations).
        for key, value in record.__dict__.items():
            if key in _LOGRECORD_RESERVED_ATTRS or value is None:
                continue
            payload[key] = value
        return json.dumps(payload, default=str)


def _configure_logging() -> None:
    handler = logging.StreamHandler()
    handler.setFormatter(_StructuredFormatter())
    root = logging.getLogger()
    if not any(isinstance(h, logging.StreamHandler) and isinstance(h.formatter, _StructuredFormatter) for h in root.handlers):
        root.addHandler(handler)
    # Gate on LOG_LEVEL so DEBUG never ships to a shared cluster by accident.
    # DEBUG in this service exposes raw JWT claim keys via token_decoder and
    # verbose per-request bootstrap-flow diagnostics via ext_authz; both are
    # local-only troubleshooting aids, not production signal.
    level_name = os.getenv("LOG_LEVEL", "INFO").upper()
    root.setLevel(getattr(logging, level_name, logging.INFO))


@asynccontextmanager
async def _lifespan(app: FastAPI) -> AsyncIterator[None]:
    _configure_logging()
    yield


def build_app() -> FastAPI:
    app = FastAPI(title="Cortex Authorization Service", lifespan=_lifespan)
    app.state.limiter = limiter
    app.add_exception_handler(RateLimitExceeded, _rate_limit_exceeded_handler)

    # instrument_fastapi installs an OTel server_request_hook that binds the
    # Cortex ctx.* attributes from request headers *before* the root request
    # span is opened. Because FastAPIInstrumentor wraps OpenTelemetryMiddleware
    # outside the user middleware stack via a build_middleware_stack override,
    # a plain add_middleware(RequestContextASGIMiddleware) cannot reach the
    # root span; the hook is the supported extension point.
    instrument_fastapi(app)

    app.include_router(health_router)
    app.include_router(ext_authz_router)
    app.include_router(admin_router)
    app.include_router(app_permissions_router)

    return app
