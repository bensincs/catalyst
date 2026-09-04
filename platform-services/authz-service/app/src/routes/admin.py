from __future__ import annotations

import logging
import os
import secrets

from fastapi import Header, APIRouter, Depends, HTTPException, Query, Security
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer

from common.dependencies import get_authz_client
from data.models import GrantPermissionResponse, RevokePermissionResponse
from data.spicedb_client import SpiceDBClient
from pydantic import BaseModel, Field

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/v1/admin")

_bearer_optional = HTTPBearer(auto_error=False)


def _verify_admin_token(
    credentials: HTTPAuthorizationCredentials | None = Security(_bearer_optional),
    hook_token: str = Header(default="", alias="X-Cortex-Hook-Token"),
) -> None:
    """Accept the admin token from Authorization, or from the hook header.

    The platform's reconciler reaches this service through the Kubernetes API
    server's proxy, because it runs outside the cluster and cannot resolve
    cluster DNS. That proxy consumes Authorization for its OWN authentication
    and never forwards it, so a call arriving that way would look
    unauthenticated no matter what it sent. Same secret and same comparison —
    only a different envelope.
    """
    expected = os.environ.get("AUTHZ_ADMIN_TOKEN", "")
    if not expected:
        raise HTTPException(status_code=503, detail="Admin token not configured")

    supplied = hook_token.strip() or (credentials.credentials if credentials else "")
    if not supplied:
        raise HTTPException(status_code=401, detail="No credential")
    if not secrets.compare_digest(supplied, expected):
        raise HTTPException(status_code=403, detail="Forbidden")


def _decode_application(resource: str, tenant_id: str) -> str | None:
    """Recover the original app id from an ``application:<objid>`` resource.

    The operator/authz encode an application object id as
    ``sanitize_object_id(app)_sanitize_tenant_id(tenant)`` (both replace ``-``
    with ``_``). Decoding strips the canonical ``_<sanitised tenant>`` suffix and
    reverses ``_``→``-`` on the remaining app fragment. App ids are kebab-case by
    convention (no underscores), so this round-trips exactly. Returns ``None`` for
    non-``application`` resources or ids that do not carry this tenant's suffix.
    """
    from data.spicedb_client import sanitize_tenant_id

    resource_type, separator, object_id = resource.partition(":")
    if separator != ":" or resource_type != "application":
        return None
    tenant_suffix = f"_{sanitize_tenant_id(tenant_id)}"
    if not object_id.endswith(tenant_suffix):
        return None
    app_fragment = object_id[: -len(tenant_suffix)]
    if not app_fragment:
        return None
    return app_fragment.replace("_", "-")


def _decode_subject_id(subject: str) -> str:
    """Recover the original user identifier from a ``user:<objid>`` subject.

    Inverse of ``sanitize_user_sub`` (``@``→``_at_``, ``.``→``_dot_``). Non-user
    subjects are returned unchanged.
    """
    object_type, separator, object_id = subject.partition(":")
    if separator != ":" or object_type != "user":
        return subject
    return object_id.replace("_at_", "@").replace("_dot_", ".")


class DecodedRelationshipItem(BaseModel):
    resource: str = Field(description="Raw SpiceDB resource, '<type>:<id>'")
    relation: str = Field(description="Relation name")
    subject: str = Field(description="Raw SpiceDB subject, '<type>:<id>'")
    application: str | None = Field(
        default=None,
        description="Decoded application id (for application resources), else null",
    )
    subject_id: str = Field(
        description="Decoded subject identifier (e.g. 'alice@example.com')"
    )


class DecodedListRelationshipsResponse(BaseModel):
    relationships: list[DecodedRelationshipItem] = Field(
        description="Tenant-scoped relationships with decoded application/subject fields"
    )
    read_at: str = Field(description="ZedToken at which this read was performed")


class RelationshipRequest(BaseModel):
    """A single relationship to write (TOUCH semantics — idempotent)."""

    resource: str = Field(
        description="Resource in the form '<type>:<id>[@<tenant>]', e.g. 'application:cortex_tenant_ui@my-tenant'"
    )
    relation: str = Field(description="Relation name, e.g. 'role'")
    subject: str = Field(
        description="Subject in the form '[<type>:]<id>', e.g. 'user:ben_at_example_dot_com'"
    )


class WriteRelationshipsRequest(BaseModel):
    tenant_id: str = Field(description="Tenant the relationships belong to")
    relationships: list[RelationshipRequest] = Field(
        description="One or more relationships to write"
    )


class WriteRelationshipsResponse(BaseModel):
    written_at: str = Field(description="ZedToken of the last write")


@router.post(
    "/relationships",
    response_model=WriteRelationshipsResponse,
    summary="Write one or more SpiceDB relationships (operator use only)",
)
async def write_relationships(
    body: WriteRelationshipsRequest,
    _: None = Depends(_verify_admin_token),
    client: SpiceDBClient = Depends(get_authz_client),
) -> WriteRelationshipsResponse:
    """Write (TOUCH) a batch of SpiceDB relationships on behalf of the operator.

    This endpoint is internal — it must only be reachable from within the
    cluster and is protected by a pre-shared bearer token (AUTHZ_ADMIN_TOKEN).
    """
    last_token = ""
    for rel in body.relationships:
        result: GrantPermissionResponse = await client.grant_permission(
            tenant_id=body.tenant_id,
            resource=rel.resource,
            relation=rel.relation,
            subject=rel.subject,
        )
        last_token = result.granted_at
        logger.info(
            "admin relationship written",
            extra={
                "tenant_id": body.tenant_id,
                "resource": rel.resource,
                "relation": rel.relation,
                "subject": rel.subject,
            },
        )
    return WriteRelationshipsResponse(written_at=last_token)


@router.get(
    "/relationships",
    response_model=DecodedListRelationshipsResponse,
    summary="List SpiceDB relationships (admin use only)",
)
async def list_relationships(
    tenant_id: str = Query(description="Tenant whose relationships to list"),
    resource_type: str = Query(default="application", description="SpiceDB resource type to filter by"),
    resource_id: str | None = Query(default=None, description="Optional specific resource ID to filter by"),
    relation: str | None = Query(default=None, description="Optional relation to filter by"),
    _: None = Depends(_verify_admin_token),
    client: SpiceDBClient = Depends(get_authz_client),
) -> DecodedListRelationshipsResponse:
    """List all SpiceDB relationships matching the given filters.

    Scoped to a tenant: only relationships whose resource ID contains the
    sanitised tenant suffix are returned. Each item carries server-decoded
    ``application`` (the original app id, for ``application:`` resources) and
    ``subject_id`` (the original user identifier) fields so clients never have
    to reverse the SpiceDB object-id encoding themselves — the authz-service
    owns the encoding and knows the tenant, making the decode unambiguous.
    """
    from data.spicedb_client import sanitize_tenant_id

    result = await client.list_relationships(
        tenant_id=tenant_id,
        resource_type=resource_type,
        resource_id=resource_id,
        relation=relation,
    )

    # Filter to only relationships that belong to this tenant (resource ID
    # ends with _<sanitised_tenant_id>).
    tenant_suffix = f"_{sanitize_tenant_id(tenant_id)}"
    decoded: list[DecodedRelationshipItem] = []
    for r in result.relationships:
        object_id = r.resource.split(":", 1)[-1]
        if not object_id.endswith(tenant_suffix):
            continue
        decoded.append(
            DecodedRelationshipItem(
                resource=r.resource,
                relation=r.relation,
                subject=r.subject,
                application=_decode_application(r.resource, tenant_id),
                subject_id=_decode_subject_id(r.subject),
            )
        )

    logger.info(
        "admin relationships listed",
        extra={"tenant_id": tenant_id, "count": len(decoded)},
    )
    return DecodedListRelationshipsResponse(relationships=decoded, read_at=result.read_at)


class SchemaRelationsResponse(BaseModel):
    definition: str = Field(description="SpiceDB definition name")
    relations: list[str] = Field(description="Relation names defined on this resource type")


# Relations exposed to clients for each SpiceDB definition. This is the subset
# of relations that tenant admins can assign or query via the admin API — not an
# exhaustive list of every relation in the schema. For example, application also
# has an `accessor` relation (retained for the bootstrap magic-link path only)
# but that is not client-assignable and is therefore omitted here.
# Update this when the client-facing relation set changes.
_SCHEMA_RELATIONS: dict[str, list[str]] = {
    "application": ["role"],
    "agent": ["accessor"],
    "tenant": ["member"],
}


@router.get(
    "/schema/relations",
    response_model=SchemaRelationsResponse,
    summary="Return the relations defined for a given SpiceDB resource type",
)
async def get_schema_relations(
    definition: str = Query(default="application", description="SpiceDB definition to inspect"),
    _: None = Depends(_verify_admin_token),
) -> SchemaRelationsResponse:
    """Return the relation names declared on a SpiceDB definition.

    This allows clients (e.g. the tenant-ui) to build dynamic role selectors
    without hardcoding schema knowledge. Update _SCHEMA_RELATIONS when the
    SpiceDB schema changes.
    """
    relations = _SCHEMA_RELATIONS.get(definition)
    if relations is None:
        raise HTTPException(status_code=404, detail=f"Unknown definition: {definition}")
    return SchemaRelationsResponse(definition=definition, relations=relations)


class SubjectsResponse(BaseModel):
    subjects: list[str] = Field(description="Distinct user subject IDs for this tenant")


@router.get(
    "/subjects",
    response_model=SubjectsResponse,
    summary="Return distinct user subjects that have any relationship in this tenant",
)
async def list_subjects(
    tenant_id: str = Query(description="Tenant to scope the subject lookup to"),
    _: None = Depends(_verify_admin_token),
    client: SpiceDBClient = Depends(get_authz_client),
) -> SubjectsResponse:
    """Return all distinct user subject IDs that appear in at least one
    relationship for the given tenant.  Used by the tenant-ui to power the
    user typeahead in the assign-access form.
    """
    from data.spicedb_client import sanitize_tenant_id

    result = await client.list_relationships(
        tenant_id=tenant_id,
        resource_type="app_role",
        relation="member",
    )
    tenant_suffix = f"_{sanitize_tenant_id(tenant_id)}"
    seen: set[str] = set()
    for r in result.relationships:
        if (
            r.resource.split(":", 1)[-1].endswith(tenant_suffix)
            and r.subject.startswith("user:")
        ):
            seen.add(r.subject.split(":", 1)[-1])
    return SubjectsResponse(subjects=sorted(seen))


class DeleteRelationshipRequest(BaseModel):
    """A single relationship to delete."""

    tenant_id: str = Field(description="Tenant the relationship belongs to")
    resource: str = Field(description="Resource in the form '<type>:<id>[@<tenant>]'")
    relation: str = Field(description="Relation name, e.g. 'role'")
    subject: str = Field(description="Subject in the form '[<type>:]<id>'")


class DeleteRelationshipResponse(BaseModel):
    revoked_at: str = Field(description="ZedToken of the delete")


@router.delete(
    "/relationships",
    response_model=DeleteRelationshipResponse,
    summary="Delete a SpiceDB relationship (admin use only)",
)
async def delete_relationship(
    body: DeleteRelationshipRequest,
    _: None = Depends(_verify_admin_token),
    client: SpiceDBClient = Depends(get_authz_client),
) -> DeleteRelationshipResponse:
    """Delete (revoke) a single SpiceDB relationship."""
    result: RevokePermissionResponse = await client.revoke_permission(
        tenant_id=body.tenant_id,
        resource=body.resource,
        relation=body.relation,
        subject=body.subject,
    )
    logger.info(
        "admin relationship deleted",
        extra={
            "tenant_id": body.tenant_id,
            "resource": body.resource,
            "relation": body.relation,
            "subject": body.subject,
        },
    )
    return DeleteRelationshipResponse(revoked_at=result.revoked_at)
