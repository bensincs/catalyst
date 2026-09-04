"""Authorization decision for an identity-aware proxy.

The ext_authz routes were written for Envoy, which hands the ORIGINAL request
to the authorization service: the app is recovered from the Host header and the
identity from the request's own bearer token.

An identity-aware proxy such as Ory Oathkeeper does not work that way. It has
already authenticated the caller, and it calls the authorizer with a request of
its own construction — so the Host header names the authorization service, not
the app being protected, and Host sniffing would identify the wrong thing (or
nothing). Asking for the app and subject explicitly removes the guesswork, and
makes the decision independent of how the caller happened to be routed.

The response shape is what such proxies expect: 200 to allow, 403 to deny.
"""

from __future__ import annotations

import logging

from fastapi import APIRouter, Depends, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel, ConfigDict, Field

from common.audit import AuditEvent, elapsed_ms, request_context_from
from common.dependencies import get_authz_client
from common.tenant import TENANT_ID
from data.spicedb_client import SpiceDBClient, SpiceDBUnavailableError, sanitize_tenant_id

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/v1/authz")

_PERMISSION = "can_access"


class DecideRequest(BaseModel):
    model_config = ConfigDict(extra="ignore")

    subject: str = Field(
        description="Caller identity, e.g. 'user:alice@example.com'. A bare "
        "identifier is treated as a user."
    )
    app: str = Field(description="Application the caller is trying to reach.")


def _normalize_subject(sub: str) -> str:
    return sub if ":" in sub else f"user:{sub}"


@router.post("/decide")
async def decide(
    body: DecideRequest,
    request: Request,
    authz: SpiceDBClient = Depends(get_authz_client),
) -> JSONResponse:
    rc = request_context_from(request)
    subject = _normalize_subject(body.subject.strip())
    app = body.app.strip()

    if not app or not body.subject.strip():
        return JSONResponse({"allowed": False}, status_code=403)

    resource = f"application:{app}@{sanitize_tenant_id(TENANT_ID)}"

    try:
        result = await authz.check_permission(
            tenant_id=TENANT_ID,
            resource=resource,
            permission=_PERMISSION,
            subject=subject,
        )
    except SpiceDBUnavailableError:
        # Fail closed. An authorization service that cannot reach its datastore
        # knows nothing about this caller, and answering "allow" would hand out
        # access precisely when the system is least able to account for it.
        AuditEvent(
            tenant_id=TENANT_ID, action="authz.decide", resource=app,
            decision="deny", reason="spicedb_unavailable",
            request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
        ).emit()
        return JSONResponse({"allowed": False}, status_code=503)

    allowed = bool(getattr(result, "allowed", result))
    AuditEvent(
        tenant_id=TENANT_ID, action="authz.decide", resource=app,
        decision="allow" if allowed else "deny",
        reason="role_membership" if allowed else "no_app_role",
        request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
        metadata={"authz_resource": resource, "permission": _PERMISSION},
    ).emit()

    if not allowed:
        return JSONResponse({"allowed": False}, status_code=403)
    return JSONResponse({"allowed": True}, status_code=200)
