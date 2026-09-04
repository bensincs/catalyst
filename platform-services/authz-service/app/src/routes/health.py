from __future__ import annotations

from fastapi import APIRouter, HTTPException

from common.dependencies import get_authz_client
from data.spicedb_client import SpiceDBUnavailableError

router = APIRouter()


@router.get("/health")
async def health() -> dict[str, str]:
    """Liveness probe — succeeds if the process is up.

    Must not depend on SpiceDB: a SpiceDB outage should not cause the pod
    to be restarted (that would amplify the outage). Readiness is reported
    by /ready.
    """
    return {"status": "ok"}


@router.get("/ready")
async def readiness() -> dict[str, str]:
    """Readiness probe — succeeds only if SpiceDB is reachable.

    Performs a CheckPermission gRPC call against a sentinel relationship
    that uses the real schema's `application#can_access` permission. SpiceDB
    returns PERMISSIONSHIP_NO_PERMISSION (allowed=False, no error) when
    no matching relationship exists — exactly what we want: the call
    succeeded, so SpiceDB is up; the boolean answer is irrelevant.
    """
    try:
        client = get_authz_client()
        await client.check_permission(
            tenant_id="readiness_probe",
            resource="application:readiness_probe",
            permission="can_access",
            subject="user:readiness-probe",
        )
    except SpiceDBUnavailableError as e:
        raise HTTPException(status_code=503, detail="SpiceDB unavailable") from e
    return {"status": "ready", "spicedb": "connected"}
