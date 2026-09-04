"""The API the admin UI calls.

Everything else in this service is machine-to-machine and authenticates with a
shared admin token. The UI cannot: it runs in a browser, and a token shipped to
a browser is a token given away. So these endpoints authorise on the identity
the gateway injected — the same X-Cortex-Sub the platform stamps on every
request reaching a protected app — and then check that person actually holds the
administrator role.

That check is the whole point. Being able to reach this service is not
permission to change who can reach anything else; without it, every user of
every hosted app could grant themselves access to all of them.
"""

from __future__ import annotations

import logging
import os

from fastapi import APIRouter, Depends, Header, HTTPException, Request
from pydantic import BaseModel, ConfigDict, Field

from common import gateway_identity
from common.audit import AuditEvent, elapsed_ms, request_context_from
from common.dependencies import get_authz_client
from common.tenant import TENANT_ID
from data.spicedb_client import SpiceDBClient, SpiceDBUnavailableError, sanitize_tenant_id

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/v1/ui")

_HDR_SUB = "x-cortex-sub"


def _admin_app() -> str:
    return os.environ.get("AUTHZ_ADMIN_APP", "authz-admin").strip() or "authz-admin"


def _normalize_subject(sub: str) -> str:
    return sub if ":" in sub else f"user:{sub}"


async def require_admin(
    request: Request,
    authorization: str = Header(default=""),
    authz: SpiceDBClient = Depends(get_authz_client),
) -> str:
    """Establish who is calling, and require that they administer app access.

    The caller's identity is taken from a VERIFIED OIDC token, not from the
    X-Cortex-* headers the gateway stamps. Those headers are only meaningful
    when the gateway set them, and this Service's ClusterIP is reachable
    without going through the gateway at all — so any pod in the cluster could
    send them itself and be believed. That was demonstrated with a plain curl
    from another namespace, which returned administrator: true.

    A token cannot be forged the same way: only a caller who actually completed
    the login holds one this tenant's identity provider will sign.
    """
    if not gateway_identity.configured():
        # Refusing is the only safe answer. Falling back to the headers would
        # reinstate exactly the hole this exists to close.
        raise HTTPException(
            status_code=503,
            detail="Token verification is not configured; refusing to trust request headers",
        )

    try:
        caller = gateway_identity.subject_from_token(authorization)
    except gateway_identity.InvalidToken as exc:
        logger.info("ui: rejected an unverified caller: %s", exc)
        raise HTTPException(status_code=401, detail="Sign in to continue") from exc

    subject = _normalize_subject(caller)
    resource = f"application:{_admin_app()}@{sanitize_tenant_id(TENANT_ID)}"
    try:
        result = await authz.check_permission(
            tenant_id=TENANT_ID, resource=resource, permission="can_access", subject=subject
        )
    except SpiceDBUnavailableError:
        raise HTTPException(status_code=503, detail="Authorization store unavailable")

    if not bool(getattr(result, "allowed", result)):
        rc = request_context_from(request)
        AuditEvent(
            tenant_id=TENANT_ID, action="ui.admin_denied", resource=_admin_app(),
            decision="deny", reason="not_an_administrator",
            request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
        ).emit()
        raise HTTPException(status_code=403, detail="You do not administer app access")
    return subject


class GrantRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    app: str = Field(description="Application to grant access to.")
    role: str = Field(default="user", description="Role to add the subject to.")
    subject: str = Field(description="Who to grant, e.g. 'ben@msft.ae'.")


@router.get("/me")
async def me(caller: str = Depends(require_admin)) -> dict:
    """Who the gateway says this is, once confirmed as an administrator."""
    return {"subject": caller, "tenant": TENANT_ID, "administrator": True}


@router.get("/apps")
async def list_apps(
    _: str = Depends(require_admin),
    authz: SpiceDBClient = Depends(get_authz_client),
) -> dict:
    """Applications that have at least one role, so can be granted."""
    from routes.app_permissions import _app_from_application_object

    result = await authz.list_relationships(
        tenant_id=TENANT_ID, resource_type="application", relation="role"
    )
    apps = {
        app
        for item in result.relationships
        if (app := _app_from_application_object(item.resource, TENANT_ID))
    }
    return {"apps": sorted(apps)}


@router.get("/apps/{app}/roles")
async def list_roles(
    app: str,
    _: str = Depends(require_admin),
    authz: SpiceDBClient = Depends(get_authz_client),
) -> dict:
    from routes.app_permissions import (
        _GRANT_RELATION,
        _ROLE_EXISTS_PERMISSION,
        _permission_object,
        _role_from_resource,
    )

    # Roles are enumerated from the reserved sentinel permission's granted_to
    # edges: a role exists for an app precisely when it was seeded against that
    # sentinel, so there is no separate list to keep in step.
    sentinel_id = _permission_object(app, _ROLE_EXISTS_PERMISSION, TENANT_ID).split(":", 1)[1]
    result = await authz.list_relationships(
        tenant_id=TENANT_ID,
        resource_type="app_permission",
        resource_id=sentinel_id,
        relation=_GRANT_RELATION,
    )
    roles = {
        role
        for item in result.relationships
        if (role := _role_from_resource(item.subject, app, TENANT_ID))
    }
    return {"roles": sorted(roles)}


@router.get("/apps/{app}/roles/{role}/members")
async def list_members(
    app: str,
    role: str,
    _: str = Depends(require_admin),
    authz: SpiceDBClient = Depends(get_authz_client),
) -> dict:
    from routes.app_permissions import (
        _MEMBER_RELATION,
        _decode_subject,
        _role_object,
    )

    role_id = _role_object(app, role, TENANT_ID).split(":", 1)[1]
    result = await authz.list_relationships(
        tenant_id=TENANT_ID,
        resource_type="app_role",
        resource_id=role_id,
        relation=_MEMBER_RELATION,
    )
    return {"members": sorted({_decode_subject(i.subject) for i in result.relationships})}


@router.post("/grant")
async def grant(
    body: GrantRequest,
    request: Request,
    caller: str = Depends(require_admin),
    authz: SpiceDBClient = Depends(get_authz_client),
) -> dict:
    from routes.app_permissions import (
        _MEMBER_RELATION,
        _ROLE_RELATION,
        _role_assignment_object,
        _role_object,
    )

    subject = _normalize_subject(body.subject.strip())
    role_object = _role_object(body.app, body.role, TENANT_ID)
    assignment = _role_assignment_object(body.app, subject, TENANT_ID)

    await authz.grant_permissions(
        relationships=[
            {"resource": role_object, "relation": _MEMBER_RELATION, "subject": subject},
            {"resource": assignment, "relation": _ROLE_RELATION, "subject": role_object},
        ],
        tenant_id=TENANT_ID,
    )
    rc = request_context_from(request)
    AuditEvent(
        tenant_id=TENANT_ID, subject=caller, action="ui.grant", resource=body.app,
        decision="allow", reason="granted_by_administrator",
        request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
        metadata={"role": body.role, "granted_to": subject},
    ).emit()
    return {"granted": True, "app": body.app, "role": body.role, "subject": subject}


@router.post("/revoke")
async def revoke(
    body: GrantRequest,
    request: Request,
    caller: str = Depends(require_admin),
    authz: SpiceDBClient = Depends(get_authz_client),
) -> dict:
    from routes.app_permissions import (
        _MEMBER_RELATION,
        _ROLE_RELATION,
        _role_assignment_object,
        _role_object,
    )

    subject = _normalize_subject(body.subject.strip())
    role_object = _role_object(body.app, body.role, TENANT_ID)
    assignment = _role_assignment_object(body.app, subject, TENANT_ID)

    await authz.revoke_permissions(
        relationships=[
            {"resource": role_object, "relation": _MEMBER_RELATION, "subject": subject},
            {"resource": assignment, "relation": _ROLE_RELATION, "subject": role_object},
        ],
        tenant_id=TENANT_ID,
    )
    rc = request_context_from(request)
    AuditEvent(
        tenant_id=TENANT_ID, subject=caller, action="ui.revoke", resource=body.app,
        decision="allow", reason="revoked_by_administrator",
        request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
        metadata={"role": body.role, "revoked_from": subject},
    ).emit()
    return {"revoked": True, "app": body.app, "role": body.role, "subject": subject}


@router.get("/access")
async def list_access(
    _: str = Depends(require_admin),
    authz: SpiceDBClient = Depends(get_authz_client),
) -> dict:
    """Every grant in the tenant, in one query.

    The UI shows people rather than pivoting on an application, so it needs the
    whole picture at once. Reading it per app would be a call per app per role;
    a role object encodes both, so listing every `member` edge answers it in
    one — and keeps the table honest, because it cannot show a stale app it
    forgot to re-query.
    """
    from routes.app_permissions import (
        _MEMBER_RELATION,
        _decode_subject,
        _split_role_object,
    )

    result = await authz.list_relationships(
        tenant_id=TENANT_ID, resource_type="app_role", relation=_MEMBER_RELATION
    )
    grants = []
    for item in result.relationships:
        parts = _split_role_object(item.resource, TENANT_ID)
        if parts is None:
            continue
        app, role = parts
        grants.append(
            {
                "subject": _decode_subject(item.subject),
                "app": app,
                "role": role,
            }
        )
    grants.sort(key=lambda g: (g["subject"], g["app"], g["role"]))
    return {"grants": grants}


@router.get("/catalog")
async def catalog(
    _: str = Depends(require_admin),
    authz: SpiceDBClient = Depends(get_authz_client),
) -> dict:
    """Every application and the roles it defines, in one query.

    The editor offers a role per application, because roles are per application
    — a previous version read the roles of the FIRST app and applied that name
    to all of them, which is wrong the moment two apps differ. Fetching them one
    app at a time would be a request per row, so they are read from the sentinel
    permission that every seeded role is granted against.
    """
    from routes.app_permissions import (
        _GRANT_RELATION,
        _ROLE_EXISTS_PERMISSION,
        _decode_fragment,
        _split_role_object,
        _tenant_suffix,
    )

    result = await authz.list_relationships(
        tenant_id=TENANT_ID, resource_type="app_permission", relation=_GRANT_RELATION
    )
    suffix = f"_{_tenant_suffix(TENANT_ID)}"
    apps: dict[str, set[str]] = {}
    for item in result.relationships:
        # Only the sentinel says "this role exists"; other permission grants are
        # about what a role can do, not which roles there are.
        object_id = item.resource.removeprefix("app_permission:")
        if not object_id.endswith(suffix):
            continue
        encoded_app, sep, encoded_perm = object_id[: -len(suffix)].partition("|")
        if sep != "|" or _decode_fragment(encoded_perm) != _ROLE_EXISTS_PERMISSION:
            continue
        parts = _split_role_object(item.subject, TENANT_ID)
        if parts is None:
            continue
        app, role = parts
        apps.setdefault(app, set()).add(role)

    return {
        "apps": [
            {"name": name, "roles": sorted(roles)} for name, roles in sorted(apps.items())
        ]
    }
