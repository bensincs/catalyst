from __future__ import annotations

import logging
import os
import re

from fastapi import APIRouter, Depends, Header, HTTPException
from pydantic import BaseModel, Field

from common.dependencies import get_authz_client
from data.models import (
    CheckPermissionResponse,
    GrantPermissionResponse,
    RevokePermissionResponse,
)
from common.tenant import TENANT_ID
from data.spicedb_client import (
    SpiceDBClient,
    SpiceDBPreconditionError,
    sanitize_object_id,
    sanitize_tenant_id,
)
from routes.admin import _verify_admin_token

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/v1/apps")

_MEMBER_RELATION = "member"
_GRANT_RELATION = "granted_to"
_ROLE_RELATION = "role"
_CHECK_PERMISSION = "check"
_ASSIGNED_PERMISSION = "assigned"
_TENANT_HEADER = "x-cortex-tenant"

# Reserved sentinel permission written by the role-ensure endpoint so a declared
# role materialises as an enumerable app_permission edge even before any member
# or real permission is granted. SpiceDB has no "empty object" concept — an
# app_role only exists once an edge references it — so deploy-time seeding needs
# a stable, idempotent edge to write. Using a sentinel app_permission (rather
# than adding a schema relation to app_role) keeps the seed a pure data write
# with zero schema/migration change. The name is namespaced under "cortex." to
# avoid colliding with any app-defined permission key. The enumerate endpoint
# (GET /roles) reads back these sentinel grants to list a tenant's roles.
_ROLE_EXISTS_PERMISSION = "cortex.role.defined"
_APP_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$")
_FRAGMENT_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
_TENANT_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$")
_BEARER_PREFIX = "bearer "


# The platform's reconciler reaches this service through the Kubernetes API
# server's proxy, which consumes the Authorization header for its own
# authentication and never forwards it. A call arriving that way carries its
# credential here instead. Same secret, same check — only a different envelope.
_HOOK_TOKEN_HEADER = "x-cortex-hook-token"


def _verify_check_token(authorization: str = Header(default="")) -> None:
    """Verify the lower-privilege runtime check token."""
    expected = os.environ.get("AUTHZ_CHECK_TOKEN") or os.environ.get(
        "AUTHZ_ADMIN_TOKEN", ""
    )
    if not expected:
        raise HTTPException(status_code=503, detail="Check token not configured")
    raw = authorization.strip()
    if not raw.lower().startswith(_BEARER_PREFIX):
        raise HTTPException(status_code=403, detail="Forbidden")
    if raw[len(_BEARER_PREFIX) :] != expected:
        raise HTTPException(status_code=403, detail="Forbidden")


def _validate_fragment(kind: str, value: str) -> str:
    pattern = _APP_PATTERN if kind == "app" else _FRAGMENT_PATTERN
    if not pattern.fullmatch(value):
        raise HTTPException(
            status_code=422,
            detail=f"{kind} must match {pattern.pattern}",
        )
    return value.lower()


def _tenant_suffix(tenant_id: str) -> str:
    if not _TENANT_PATTERN.fullmatch(tenant_id):
        raise HTTPException(
            status_code=422,
            detail=f"tenant_id must match {_TENANT_PATTERN.pattern}",
        )
    return sanitize_tenant_id(tenant_id)


def _encode_fragment(kind: str, value: str) -> str:
    cleaned = _validate_fragment(kind, value)
    return (
        cleaned.replace("_", "__")
        .replace(".", "_dot_")
        .replace("-", "_dash_")
    )


def _decode_fragment(value: str) -> str:
    decoded: list[str] = []
    idx = 0
    while idx < len(value):
        if value.startswith("_dot_", idx):
            decoded.append(".")
            idx += len("_dot_")
        elif value.startswith("_dash_", idx):
            decoded.append("-")
            idx += len("_dash_")
        elif value.startswith("__", idx):
            decoded.append("_")
            idx += len("__")
        else:
            decoded.append(value[idx])
            idx += 1
    return "".join(decoded)


def _role_object(app: str, role: str, tenant_id: str) -> str:
    return (
        f"app_role:{_encode_fragment('app', app)}|"
        f"{_encode_fragment('role', role)}_{_tenant_suffix(tenant_id)}"
    )


def _permission_object(app: str, permission: str, tenant_id: str) -> str:
    return (
        f"app_permission:{_encode_fragment('app', app)}|"
        f"{_encode_fragment('permission', permission)}_{_tenant_suffix(tenant_id)}"
    )


def _subject_assignment_fragment(subject: str) -> str:
    if ":" not in subject:
        subject = f"user:{subject}"
    return (
        subject
        .replace("_", "__")
        .replace(":", "_colon_")
        .replace("@", "_at_")
        .replace(".", "_dot_")
        .replace("-", "_dash_")
    )


def _role_assignment_object(app: str, subject: str, tenant_id: str) -> str:
    return (
        f"app_role_assignment:{_encode_fragment('app', app)}|"
        f"{_subject_assignment_fragment(subject)}_{_tenant_suffix(tenant_id)}"
    )


def _split_role_object(resource: str, tenant_id: str) -> tuple[str, str] | None:
    """Recover (app, role) from an ``app_role`` object.

    The object id encodes both, so a single listing of every member edge can be
    turned back into "who has what, where" without knowing the applications in
    advance — which is what lets the admin UI show every grant in one query.
    """
    prefix = "app_role:"
    if not resource.startswith(prefix):
        return None
    tenant_suffix = f"_{_tenant_suffix(tenant_id)}"
    object_id = resource.removeprefix(prefix)
    if not object_id.endswith(tenant_suffix):
        return None
    scoped_id = object_id[: -len(tenant_suffix)]
    encoded_app, separator, encoded_role = scoped_id.partition("|")
    if separator != "|":
        return None
    return _decode_fragment(encoded_app), _decode_fragment(encoded_role)


def _role_from_resource(resource: str, app: str, tenant_id: str) -> str | None:
    prefix = "app_role:"
    if not resource.startswith(prefix):
        return None
    tenant_suffix = f"_{_tenant_suffix(tenant_id)}"
    object_id = resource.removeprefix(prefix)
    if not object_id.endswith(tenant_suffix):
        return None
    scoped_id = object_id[: -len(tenant_suffix)]
    encoded_app, separator, encoded_role = scoped_id.partition("|")
    if separator != "|" or encoded_app != _encode_fragment("app", app):
        return None
    return _decode_fragment(encoded_role)


def _app_from_sentinel_resource(resource: str, tenant_id: str) -> str | None:
    """Return the app name from an ``app_permission`` sentinel object, else None.

    Used to enumerate which apps have any seeded role in a tenant: an app
    materialises its sentinel ``app_permission:<app>|cortex.role.defined_<tenant>``
    as soon as a role is ensured. Only objects whose tenant suffix matches and
    whose permission fragment is the sentinel are accepted; others (real app
    permissions, other tenants) return None.
    """
    prefix = "app_permission:"
    if not resource.startswith(prefix):
        return None
    tenant_suffix = f"_{_tenant_suffix(tenant_id)}"
    object_id = resource.removeprefix(prefix)
    if not object_id.endswith(tenant_suffix):
        return None
    scoped_id = object_id[: -len(tenant_suffix)]
    encoded_app, separator, encoded_perm = scoped_id.partition("|")
    if separator != "|":
        return None
    if encoded_perm != _encode_fragment("permission", _ROLE_EXISTS_PERMISSION):
        return None
    return _decode_fragment(encoded_app)


def _permission_from_resource(resource: str, app: str, tenant_id: str) -> str | None:
    """Return the permission name from an ``app_permission`` object, else None.

    Skips the reserved ``cortex.role.defined`` sentinel and objects for other
    apps/tenants, so the result is the set of real permissions on a role.
    """
    prefix = "app_permission:"
    if not resource.startswith(prefix):
        return None
    tenant_suffix = f"_{_tenant_suffix(tenant_id)}"
    object_id = resource.removeprefix(prefix)
    if not object_id.endswith(tenant_suffix):
        return None
    scoped_id = object_id[: -len(tenant_suffix)]
    encoded_app, separator, encoded_perm = scoped_id.partition("|")
    if separator != "|" or encoded_app != _encode_fragment("app", app):
        return None
    perm = _decode_fragment(encoded_perm)
    if perm == _ROLE_EXISTS_PERMISSION:
        return None
    return perm


def _decode_subject(subject: str) -> str:
    """Decode a SpiceDB subject object back to a readable identifier.

    The inverse of ``sanitize_user_sub`` (the encoding ``grant_permission``
    applies to a ``user:<sub>`` subject): ``@``→``_at_`` and ``.``→``_dot_``
    only. Returns e.g. ``user:alice@example.com``. Subjects whose type is not
    ``user`` are returned unchanged.
    """
    object_type, separator, object_id = subject.partition(":")
    if separator != ":" or object_type != "user":
        return subject
    decoded = object_id.replace("_at_", "@").replace("_dot_", ".")
    return f"{object_type}:{decoded}"


def _resolve_tenant(header_tenant: str, body_tenant: str | None = None) -> str:
    """Resolve the tenant for a roles/permissions call.

    This deployment serves exactly one tenant, so the tenant is configuration
    rather than something a caller supplies. The header and body are still
    honoured when present — but only to REJECT a mismatch, never to select a
    different tenant. Callers that omit them (the admin UI, which has no reason
    to know a tenant id) get the configured one.
    """
    supplied = header_tenant or body_tenant
    if supplied and supplied != TENANT_ID:
        raise HTTPException(
            status_code=403,
            detail=f"tenant_id does not match the tenant this deployment serves",
        )
    if body_tenant is not None and header_tenant and body_tenant != header_tenant:
        raise HTTPException(
            status_code=403,
            detail=f"tenant_id does not match {_TENANT_HEADER} header",
        )
    _tenant_suffix(TENANT_ID)
    return TENANT_ID


class AddRoleMemberRequest(BaseModel):
    tenant_id: str | None = Field(
        default=None,
        description=(
            "Tenant. Optional: this deployment serves exactly one tenant, so a "
            "caller with no reason to know its id may omit this. When supplied "
            "it must name the tenant this deployment serves — it selects "
            "nothing, it only has to agree."
        ),
    )
    subject: str = Field(description="Subject, e.g. 'user:alice@example.com'")


class RemoveRoleMemberRequest(BaseModel):
    tenant_id: str | None = Field(
        default=None,
        description=(
            "Tenant. Optional: this deployment serves exactly one tenant, so a "
            "caller with no reason to know its id may omit this. When supplied "
            "it must name the tenant this deployment serves — it selects "
            "nothing, it only has to agree."
        ),
    )
    subject: str = Field(description="Subject, e.g. 'user:alice@example.com'")


class GrantRolePermissionRequest(BaseModel):
    tenant_id: str | None = Field(
        default=None,
        description=(
            "Tenant. Optional: this deployment serves exactly one tenant, so a "
            "caller with no reason to know its id may omit this. When supplied "
            "it must name the tenant this deployment serves — it selects "
            "nothing, it only has to agree."
        ),
    )
    permission: str = Field(description="App permission name, e.g. 'report.manage'")


class RemoveRolePermissionRequest(BaseModel):
    tenant_id: str | None = Field(
        default=None,
        description=(
            "Tenant. Optional: this deployment serves exactly one tenant, so a "
            "caller with no reason to know its id may omit this. When supplied "
            "it must name the tenant this deployment serves — it selects "
            "nothing, it only has to agree."
        ),
    )
    permission: str = Field(description="App permission name, e.g. 'report.manage'")


class CheckAppPermissionRequest(BaseModel):
    tenant_id: str | None = Field(
        default=None,
        description=(
            "Tenant. Optional: this deployment serves exactly one tenant, so a "
            "caller with no reason to know its id may omit this. When supplied "
            "it must name the tenant this deployment serves — it selects "
            "nothing, it only has to agree."
        ),
    )
    permission: str = Field(description="App permission name, e.g. 'report.manage'")
    subject: str = Field(description="Subject, e.g. 'user:alice@example.com'")


class ListSubjectRolesRequest(BaseModel):
    tenant_id: str | None = Field(
        default=None,
        description=(
            "Tenant. Optional: this deployment serves exactly one tenant, so a "
            "caller with no reason to know its id may omit this. When supplied "
            "it must name the tenant this deployment serves — it selects "
            "nothing, it only has to agree."
        ),
    )
    subject: str = Field(description="Subject, e.g. 'user:alice@example.com'")


class ListSubjectRolesResponse(BaseModel):
    roles: list[str] = Field(description="App roles assigned to the subject")


class EnsureRoleRequest(BaseModel):
    tenant_id: str | None = Field(
        default=None,
        description=(
            "Tenant. Optional: this deployment serves exactly one tenant, so a "
            "caller with no reason to know its id may omit this. When supplied "
            "it must name the tenant this deployment serves — it selects "
            "nothing, it only has to agree."
        ),
    )


class ListRolesResponse(BaseModel):
    roles: list[str] = Field(
        description="App roles seeded for this app in this tenant"
    )


class ListRoleMembersResponse(BaseModel):
    members: list[str] = Field(
        description="Subjects assigned to the role, e.g. 'user:alice@example.com'"
    )


class ListRolePermissionsResponse(BaseModel):
    permissions: list[str] = Field(
        description="Permission keys granted to the role (platform-owned taxonomy)"
    )


class ListAppsResponse(BaseModel):
    apps: list[str] = Field(
        description="Application ids that have at least one seeded role in this tenant"
    )


class ListAllAppsResponse(BaseModel):
    apps: list[str] = Field(
        description="All application ids the tenant has (any application#role object), decoded"
    )


class PurgeAppResponse(BaseModel):
    app: str = Field(description="The application id that was purged")
    tenant_id: str = Field(description="The tenant the app was purged from")
    objects_deleted: int = Field(
        description="Count of app_role/app_permission/app_role_assignment objects removed"
    )
    roles_unlinked: bool = Field(
        description=(
            "Whether the application object's role, bootstrap, and legacy accessor edges were removed. "
            "role edges (written by ensure_role) are removed to sever can_access; "
            "bootstrap access and stale accessor edges are also removed as cleanup."
        )
    )


@router.get(
    "",
    response_model=ListAppsResponse,
    dependencies=[Depends(_verify_admin_token)],
)
async def list_apps(
    x_cortex_tenant: str = Header(default=""),
    client: SpiceDBClient = Depends(get_authz_client),
) -> ListAppsResponse:
    """Enumerate the apps that have any seeded role in this tenant.

    Scans every ``app_permission`` object with a ``granted_to`` edge and keeps
    those whose object id is the reserved ``cortex.role.defined`` sentinel for
    this tenant, decoding the app fragment. Lets the tenant-ui populate its
    application picker with the apps that actually have roles to manage, without
    coupling the UI to Kubernetes.
    """
    tenant = _resolve_tenant(x_cortex_tenant)
    result = await client.list_relationships(
        tenant_id=tenant,
        resource_type="app_permission",
        relation=_GRANT_RELATION,
    )
    apps = {
        app
        for item in result.relationships
        if (app := _app_from_sentinel_resource(item.resource, tenant))
    }
    logger.info(
        "apps with roles listed",
        extra={"tenant_id": tenant, "count": len(apps)},
    )
    return ListAppsResponse(apps=sorted(apps))


def _app_from_application_object(resource: str, tenant_id: str) -> str | None:
    """Decode an ``application:<app>_<tenant>`` object back to the app id.

    Strips the canonical ``_<sanitised tenant>`` suffix and reverses ``_``→``-``
    (app ids are kebab-case, so this round-trips). Returns ``None`` for other
    resource types or ids that do not carry this tenant's suffix.
    """
    prefix = "application:"
    if not resource.startswith(prefix):
        return None
    tenant_suffix = f"_{_tenant_suffix(tenant_id)}"
    object_id = resource.removeprefix(prefix)
    if not object_id.endswith(tenant_suffix):
        return None
    app_fragment = object_id[: -len(tenant_suffix)]
    if not app_fragment:
        return None
    return app_fragment.replace("_", "-")


@router.get(
    "/all",
    response_model=ListAllAppsResponse,
    dependencies=[Depends(_verify_admin_token)],
)
async def list_all_apps(
    x_cortex_tenant: str = Header(default=""),
    client: SpiceDBClient = Depends(get_authz_client),
) -> ListAllAppsResponse:
    """Enumerate every application the tenant has, decoded to readable app ids.

    Scans every ``application`` object carrying a ``role`` edge for this tenant
    and decodes each object id back to its app id. This is the complete set of
    apps the tenant has at least one seeded role for — so the tenant-ui can
    populate its application picker without hardcoding a known-apps list or
    coupling to Kubernetes.
    """
    tenant = _resolve_tenant(x_cortex_tenant)
    result = await client.list_relationships(
        tenant_id=tenant,
        resource_type="application",
        relation="role",
    )
    apps = {
        app
        for item in result.relationships
        if (app := _app_from_application_object(item.resource, tenant))
    }
    logger.info(
        "all apps listed",
        extra={"tenant_id": tenant, "count": len(apps)},
    )
    return ListAllAppsResponse(apps=sorted(apps))


@router.delete(
    "/{app}",
    response_model=PurgeAppResponse,
    dependencies=[Depends(_verify_admin_token)],
)
async def purge_app(
    app: str,
    x_cortex_tenant: str = Header(default=""),
    client: SpiceDBClient = Depends(get_authz_client),
) -> PurgeAppResponse:
    """Delete an application's entire SpiceDB footprint for a tenant.

    Removes every object the role pipeline writes for this app/tenant — the
    ``app_role`` objects (and their ``member`` edges), the ``app_permission``
    objects (real permissions and the ``cortex.role.defined`` sentinel), the
    ``app_role_assignment`` objects — plus bootstrap and stale accessor edges.
    Called by the tenant-operator finalizer when an ApplicationDeployment
    is deleted, so a removed app leaves no orphaned authz state (roles, members,
    or gateway access).

    Idempotent: enumerates the app's objects and deletes each by exact id, so a
    re-call (or an app that was never seeded) is a harmless no-op returning 200.
    """
    tenant = _resolve_tenant(x_cortex_tenant)
    encoded_app = _encode_fragment("app", app)
    app_prefix = f"{encoded_app}|"
    tenant_suffix = f"_{_tenant_suffix(tenant)}"

    # Collect the distinct object ids belonging to this app+tenant across the
    # three app-scoped definitions, then delete each object wholesale.
    object_ids: dict[str, set[str]] = {
        "app_role": set(),
        "app_permission": set(),
        "app_role_assignment": set(),
    }
    for resource_type in object_ids:
        result = await client.list_relationships(
            tenant_id=tenant,
            resource_type=resource_type,
        )
        for item in result.relationships:
            object_id = item.resource.split(":", 1)[-1]
            if object_id.startswith(app_prefix) and object_id.endswith(tenant_suffix):
                object_ids[resource_type].add(object_id)

    objects_deleted = 0
    for resource_type, ids in object_ids.items():
        for object_id in ids:
            await client.delete_object(
                resource_type=resource_type,
                object_id=object_id,
            )
            objects_deleted += 1

    # Remove the application object's legacy accessor, bootstrap, and role edges.
    # The object id uses the same encoding as the ext-authz route and the
    # accessor-grant path: sanitize(app)_sanitize(tenant).
    accessor_object_id = (
        f"{sanitize_object_id(app)}_{sanitize_tenant_id(tenant)}"
    )
    await client.delete_object(
        resource_type="application",
        object_id=accessor_object_id,
        relation="accessor",
    )
    await client.delete_object(
        resource_type="application",
        object_id=accessor_object_id,
        relation="bootstrap_access",
    )
    # Remove the role→app_role links written by ensure_role.
    await client.delete_object(
        resource_type="application",
        object_id=accessor_object_id,
        relation="role",
    )

    logger.info(
        "app purged",
        extra={
            "app": app,
            "tenant_id": tenant,
            "objects_deleted": objects_deleted,
        },
    )
    return PurgeAppResponse(
        app=app,
        tenant_id=tenant,
        objects_deleted=objects_deleted,
        roles_unlinked=True,
    )


@router.put(
    "/{app}/roles/{role}",
    response_model=GrantPermissionResponse,
    dependencies=[Depends(_verify_admin_token)],
)
async def ensure_role(
    app: str,
    role: str,
    body: EnsureRoleRequest,
    x_cortex_tenant: str = Header(default=""),
    client: SpiceDBClient = Depends(get_authz_client),
) -> GrantPermissionResponse:
    """Idempotently ensure an app role exists for a tenant.

    Writes two edges atomically in a single SpiceDB WriteRelationships call
    (TOUCH — idempotent on retry):

    1. ``app_permission:<app>|cortex.role.defined_<tenant>#granted_to@app_role:<id>``
       — the sentinel that materialises the role as enumerable.
    2. ``application:<app>@<tenant>#role@app_role:<id>``
       — links the application to the role so that any member of the role
       automatically gains ``can_access`` on the application via the schema
       ``can_access = bootstrap_access + role->member`` path.

    Both edges are written in one call so there is no partial-write window
    where the role is enumerable but can_access is unresolvable. Used by the
    tenant-operator to seed an application's declared role set at deployment
    time. Assigning users to roles stays a separate, tenant-admin concern.
    """
    tenant = _resolve_tenant(x_cortex_tenant, body.tenant_id)
    resource = _permission_object(app, _ROLE_EXISTS_PERMISSION, tenant)
    role_subject = _role_object(app, role, tenant)

    # We pass the raw "<app>@<tenant>" form for the application resource and
    # rely on _parse_resource inside grant_permissions to apply
    # sanitize_object_id(app) + "_" + sanitize_tenant_id(tenant), which is
    # identical to what purge_app uses when deleting role edges — so the two
    # paths always target the same SpiceDB object id.
    application_resource = f"application:{app}@{tenant}"

    result = await client.grant_permissions(
        tenant_id=tenant,
        relationships=[
            {"resource": resource, "relation": _GRANT_RELATION, "subject": role_subject},
            {"resource": application_resource, "relation": "role", "subject": role_subject},
        ],
    )

    logger.info(
        "app role ensured",
        extra={"app": app, "role": role, "tenant_id": tenant, "resource": resource},
    )
    return result


@router.get(
    "/{app}/roles",
    response_model=ListRolesResponse,
    dependencies=[Depends(_verify_admin_token)],
)
async def list_roles(
    app: str,
    x_cortex_tenant: str = Header(default=""),
    client: SpiceDBClient = Depends(get_authz_client),
) -> ListRolesResponse:
    """Enumerate the app roles seeded for this app in this tenant.

    Reads the ``granted_to`` edges on the reserved sentinel permission
    (``cortex.role.defined``) — every role seeded via ``ensure_role`` (or that
    has been granted any permission) is a ``granted_to`` subject there — and
    decodes each ``app_role`` object back to its readable role name. Used by the
    tenant-ui Access Control page to list assignable roles.
    """
    tenant = _resolve_tenant(x_cortex_tenant)
    sentinel = _permission_object(app, _ROLE_EXISTS_PERMISSION, tenant)
    sentinel_id = sentinel.split(":", 1)[1]
    result = await client.list_relationships(
        tenant_id=tenant,
        resource_type="app_permission",
        resource_id=sentinel_id,
        relation=_GRANT_RELATION,
    )
    roles = {
        role
        for item in result.relationships
        if (role := _role_from_resource(item.subject, app, tenant))
    }
    logger.info(
        "app roles listed",
        extra={"app": app, "tenant_id": tenant, "count": len(roles)},
    )
    return ListRolesResponse(roles=sorted(roles))


@router.get(
    "/{app}/roles/{role}/members",
    response_model=ListRoleMembersResponse,
    dependencies=[Depends(_verify_admin_token)],
)
async def list_role_members(
    app: str,
    role: str,
    x_cortex_tenant: str = Header(default=""),
    client: SpiceDBClient = Depends(get_authz_client),
) -> ListRoleMembersResponse:
    """List the subjects assigned to a role for this app in this tenant.

    Reads the ``member`` edges on the ``app_role`` object and decodes each
    subject back to a readable identifier. Used by the tenant-ui Access Control
    page to show who holds each role.
    """
    tenant = _resolve_tenant(x_cortex_tenant)
    role_object = _role_object(app, role, tenant)
    role_id = role_object.split(":", 1)[1]
    result = await client.list_relationships(
        tenant_id=tenant,
        resource_type="app_role",
        resource_id=role_id,
        relation=_MEMBER_RELATION,
    )
    members = sorted(
        {_decode_subject(item.subject) for item in result.relationships}
    )
    logger.info(
        "app role members listed",
        extra={
            "app": app,
            "role": role,
            "tenant_id": tenant,
            "count": len(members),
        },
    )
    return ListRoleMembersResponse(members=members)


@router.get(
    "/{app}/roles/{role}/permissions",
    response_model=ListRolePermissionsResponse,
    dependencies=[Depends(_verify_admin_token)],
)
async def list_role_permissions(
    app: str,
    role: str,
    x_cortex_tenant: str = Header(default=""),
    client: SpiceDBClient = Depends(get_authz_client),
) -> ListRolePermissionsResponse:
    """List the permission keys granted to a role for this app in this tenant.

    Reads the ``app_permission`` objects whose ``granted_to`` edge points at the
    role and decodes each permission name (excluding the reserved sentinel).
    Read-only view of the platform-owned role→permission taxonomy; the tenant-ui
    Roles tab uses it. Permissions are defined in the app catalog and seeded by
    the operator — they are not editable here.
    """
    tenant = _resolve_tenant(x_cortex_tenant)
    role_object = _role_object(app, role, tenant)
    result = await client.list_relationships(
        tenant_id=tenant,
        resource_type="app_permission",
        relation=_GRANT_RELATION,
        subject=role_object,
    )
    permissions = sorted(
        {
            perm
            for item in result.relationships
            if (perm := _permission_from_resource(item.resource, app, tenant))
        }
    )
    logger.info(
        "role permissions listed",
        extra={
            "app": app,
            "role": role,
            "tenant_id": tenant,
            "count": len(permissions),
        },
    )
    return ListRolePermissionsResponse(permissions=permissions)


@router.post(
    "/{app}/roles/{role}/members",
    response_model=GrantPermissionResponse,
    dependencies=[Depends(_verify_admin_token)],
)
async def add_role_member(
    app: str,
    role: str,
    body: AddRoleMemberRequest,
    x_cortex_tenant: str = Header(default=""),
    client: SpiceDBClient = Depends(get_authz_client),
) -> GrantPermissionResponse:
    tenant = _resolve_tenant(x_cortex_tenant, body.tenant_id)
    resource = _role_object(app, role, tenant)
    sentinel = _permission_object(app, _ROLE_EXISTS_PERMISSION, tenant)
    try:
        assignment = _role_assignment_object(app, body.subject, tenant)
        result = await client.grant_permissions(
            tenant_id=tenant,
            relationships=[
                {"resource": resource, "relation": _MEMBER_RELATION, "subject": body.subject},
                {"resource": assignment, "relation": _ROLE_RELATION, "subject": resource},
            ],
            preconditions=[
                {"resource": sentinel, "relation": _GRANT_RELATION, "subject": resource}
            ],
        )
    except SpiceDBPreconditionError:
        raise HTTPException(status_code=404, detail=f"Role not found: {role}")
    logger.info(
        "app role member added",
        extra={
            "resource": resource,
            "relation": _MEMBER_RELATION,
            "subject": body.subject,
        },
    )
    return result


@router.post(
    "/{app}/roles/list-for-subject",
    response_model=ListSubjectRolesResponse,
    dependencies=[Depends(_verify_check_token)],
)
async def list_roles_for_subject(
    app: str,
    body: ListSubjectRolesRequest,
    x_cortex_tenant: str = Header(default=""),
    client: SpiceDBClient = Depends(get_authz_client),
) -> ListSubjectRolesResponse:
    tenant = _resolve_tenant(x_cortex_tenant, body.tenant_id)
    assignment = _role_assignment_object(app, body.subject, tenant)
    result = await client.lookup_subjects(
        tenant_id=tenant,
        resource=assignment,
        permission=_ASSIGNED_PERMISSION,
        subject_object_type="app_role",
    )

    roles = {
        role
        for subject in result.subjects
        if (role := _role_from_resource(subject, app, tenant))
    }
    logger.info(
        "app roles listed for subject",
        extra={
            "app": app,
            "tenant_id": tenant,
            "subject": body.subject,
            "count": len(roles),
        },
    )
    return ListSubjectRolesResponse(roles=sorted(roles))


@router.delete(
    "/{app}/roles/{role}/members",
    response_model=RevokePermissionResponse,
    dependencies=[Depends(_verify_admin_token)],
)
async def remove_role_member(
    app: str,
    role: str,
    body: RemoveRoleMemberRequest,
    x_cortex_tenant: str = Header(default=""),
    client: SpiceDBClient = Depends(get_authz_client),
) -> RevokePermissionResponse:
    tenant = _resolve_tenant(x_cortex_tenant, body.tenant_id)
    resource = _role_object(app, role, tenant)
    assignment = _role_assignment_object(app, body.subject, tenant)
    result = await client.revoke_permissions(
        tenant_id=tenant,
        relationships=[
            {"resource": resource, "relation": _MEMBER_RELATION, "subject": body.subject},
            {"resource": assignment, "relation": _ROLE_RELATION, "subject": resource},
        ],
    )
    logger.info(
        "app role member removed",
        extra={
            "resource": resource,
            "relation": _MEMBER_RELATION,
            "subject": body.subject,
        },
    )
    return result


@router.post(
    "/{app}/roles/{role}/permissions",
    response_model=GrantPermissionResponse,
    dependencies=[Depends(_verify_admin_token)],
)
async def grant_role_permission(
    app: str,
    role: str,
    body: GrantRolePermissionRequest,
    x_cortex_tenant: str = Header(default=""),
    client: SpiceDBClient = Depends(get_authz_client),
) -> GrantPermissionResponse:
    tenant = _resolve_tenant(x_cortex_tenant, body.tenant_id)
    resource = _permission_object(app, body.permission, tenant)
    role_subject = _role_object(app, role, tenant)
    result = await client.grant_permission(
        tenant_id=tenant,
        resource=resource,
        relation=_GRANT_RELATION,
        subject=role_subject,
    )
    logger.info(
        "app permission granted to role",
        extra={
            "resource": resource,
            "relation": _GRANT_RELATION,
            "subject": role_subject,
        },
    )
    return result


@router.delete(
    "/{app}/roles/{role}/permissions",
    response_model=RevokePermissionResponse,
    dependencies=[Depends(_verify_admin_token)],
)
async def remove_role_permission(
    app: str,
    role: str,
    body: RemoveRolePermissionRequest,
    x_cortex_tenant: str = Header(default=""),
    client: SpiceDBClient = Depends(get_authz_client),
) -> RevokePermissionResponse:
    tenant = _resolve_tenant(x_cortex_tenant, body.tenant_id)
    resource = _permission_object(app, body.permission, tenant)
    role_subject = _role_object(app, role, tenant)
    result = await client.revoke_permission(
        tenant_id=tenant,
        resource=resource,
        relation=_GRANT_RELATION,
        subject=role_subject,
    )
    logger.info(
        "app permission revoked from role",
        extra={
            "resource": resource,
            "relation": _GRANT_RELATION,
            "subject": role_subject,
        },
    )
    return result


@router.post(
    "/{app}/permissions/check",
    response_model=CheckPermissionResponse,
    dependencies=[Depends(_verify_check_token)],
)
async def check_app_permission(
    app: str,
    body: CheckAppPermissionRequest,
    x_cortex_tenant: str = Header(default=""),
    client: SpiceDBClient = Depends(get_authz_client),
) -> CheckPermissionResponse:
    tenant = _resolve_tenant(x_cortex_tenant, body.tenant_id)
    resource = _permission_object(app, body.permission, tenant)
    result = await client.check_permission(
        tenant_id=tenant,
        resource=resource,
        permission=_CHECK_PERMISSION,
        subject=body.subject,
    )
    logger.info(
        "app permission checked",
        extra={
            "resource": resource,
            "subject": body.subject,
            "allowed": result.allowed,
        },
    )
    return result
