from __future__ import annotations

import pathlib
from unittest.mock import AsyncMock, call

import pytest
import pytest_asyncio
from httpx import ASGITransport, AsyncClient

from data.models import (
    CheckPermissionResponse,
    ListRelationshipsResponse,
    LookupSubjectsResponse,
    RelationshipItem,
)
from tests.conftest import ZEDTOKEN_READ, ZEDTOKEN_WRITE

ADMIN_TOKEN = "test-admin-token"
# The tenant this deployment serves. It must match the one the app is
# configured with (conftest sets AUTHZ_TENANT_ID): a single-tenant deployment
# refuses a request naming any other tenant rather than serving it.
TENANT = "tenant-alpha"
AUTH = {"authorization": f"Bearer {ADMIN_TOKEN}"}
TENANT_HEADER = {"x-cortex-tenant": TENANT}
HEADERS = {**AUTH, **TENANT_HEADER}

ROLE_OBJ = "app_role:insight|admin_tenant_a"
PERM_OBJ = "app_permission:insight|report_dot_manage_tenant_a"
APP_ACCESS_OBJ = "application:insight@tenant-a"
SUBJECT = "user:alice@example.com"
ASSIGNMENT_OBJ = "app_role_assignment:insight|user_colon_alice_at_example_dot_com_tenant_a"


@pytest_asyncio.fixture
async def apps_client(
    mock_spicedb_client: AsyncMock, monkeypatch: pytest.MonkeyPatch
) -> AsyncClient:
    monkeypatch.setenv("AUTHZ_ADMIN_TOKEN", ADMIN_TOKEN)
    from app.main import build_app
    from common.dependencies import get_authz_client

    app = build_app()
    app.dependency_overrides[get_authz_client] = lambda: mock_spicedb_client
    async with AsyncClient(
        transport=ASGITransport(app=app), base_url="http://test"
    ) as ac:
        yield ac


@pytest.mark.asyncio
async def test_add_member_writes_canonical_role_object(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    resp = await apps_client.post(
        "/v1/apps/insight/roles/admin/members",
        json={"tenant_id": TENANT, "subject": SUBJECT},
        headers=HEADERS,
    )
    assert resp.status_code == 200
    assert resp.json()["granted_at"] == ZEDTOKEN_WRITE
    # Accessor write was removed: role membership alone confers gateway access
    # via the schema's can_access = bootstrap_access + role->member path.
    mock_spicedb_client.grant_permissions.assert_awaited_once_with(
        tenant_id=TENANT,
        relationships=[
            {"resource": ROLE_OBJ, "relation": "member", "subject": SUBJECT},
            {"resource": ASSIGNMENT_OBJ, "relation": "role", "subject": ROLE_OBJ},
        ],
        preconditions=[
            {
                "resource": SENTINEL_PERM_OBJ,
                "relation": "granted_to",
                "subject": ROLE_OBJ,
            }
        ],
    )


@pytest.mark.asyncio
async def test_add_member_rejects_unseeded_role(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    from data.spicedb_client import SpiceDBPreconditionError

    mock_spicedb_client.grant_permissions.side_effect = SpiceDBPreconditionError()

    resp = await apps_client.post(
        "/v1/apps/insight/roles/missing/members",
        json={"tenant_id": TENANT, "subject": SUBJECT},
        headers=HEADERS,
    )

    assert resp.status_code == 404
    assert resp.json()["detail"] == "Role not found: missing"
    mock_spicedb_client.grant_permissions.assert_awaited_once()


@pytest.mark.asyncio
async def test_remove_member(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    resp = await apps_client.request(
        "DELETE",
        "/v1/apps/insight/roles/admin/members",
        json={"tenant_id": TENANT, "subject": SUBJECT},
        headers=HEADERS,
    )
    assert resp.status_code == 200
    mock_spicedb_client.revoke_permissions.assert_awaited_once_with(
        tenant_id=TENANT,
        relationships=[
            {"resource": ROLE_OBJ, "relation": "member", "subject": SUBJECT},
            {"resource": ASSIGNMENT_OBJ, "relation": "role", "subject": ROLE_OBJ},
        ],
    )


@pytest.mark.asyncio
async def test_grant_permission_uses_consistent_role_subject(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    resp = await apps_client.post(
        "/v1/apps/insight/roles/admin/permissions",
        json={"tenant_id": TENANT, "permission": "report.manage"},
        headers=HEADERS,
    )
    assert resp.status_code == 200
    mock_spicedb_client.grant_permission.assert_awaited_once_with(
        tenant_id=TENANT,
        resource=PERM_OBJ,
        relation="granted_to",
        subject=ROLE_OBJ,
    )
    assert "." not in PERM_OBJ and "." not in ROLE_OBJ


@pytest.mark.asyncio
async def test_remove_permission(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    resp = await apps_client.request(
        "DELETE",
        "/v1/apps/insight/roles/admin/permissions",
        json={"tenant_id": TENANT, "permission": "report.manage"},
        headers=HEADERS,
    )
    assert resp.status_code == 200
    mock_spicedb_client.revoke_permission.assert_awaited_once_with(
        tenant_id=TENANT,
        resource=PERM_OBJ,
        relation="granted_to",
        subject=ROLE_OBJ,
    )


@pytest.mark.asyncio
async def test_check_allowed(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    resp = await apps_client.post(
        "/v1/apps/insight/permissions/check",
        json={"tenant_id": TENANT, "permission": "report.manage", "subject": SUBJECT},
        headers=HEADERS,
    )
    assert resp.status_code == 200
    assert resp.json() == {"allowed": True, "checked_at": ZEDTOKEN_READ}
    mock_spicedb_client.check_permission.assert_awaited_once_with(
        tenant_id=TENANT,
        resource=PERM_OBJ,
        permission="check",
        subject=SUBJECT,
    )


@pytest.mark.asyncio
async def test_check_denied(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    mock_spicedb_client.check_permission = AsyncMock(
        return_value=CheckPermissionResponse(allowed=False, checked_at=ZEDTOKEN_READ)
    )
    resp = await apps_client.post(
        "/v1/apps/insight/permissions/check",
        json={"tenant_id": TENANT, "permission": "report.manage", "subject": SUBJECT},
        headers=HEADERS,
    )
    assert resp.status_code == 200
    assert resp.json()["allowed"] is False


@pytest.mark.asyncio
async def test_tenant_mismatch_rejected(apps_client: AsyncClient) -> None:
    resp = await apps_client.post(
        "/v1/apps/insight/roles/admin/members",
        json={"tenant_id": "tenant-b", "subject": SUBJECT},
        headers=HEADERS,
    )
    assert resp.status_code == 403


@pytest.mark.asyncio
async def test_missing_tenant_header_rejected(apps_client: AsyncClient) -> None:
    resp = await apps_client.post(
        "/v1/apps/insight/roles/admin/members",
        json={"tenant_id": TENANT, "subject": SUBJECT},
        headers=AUTH,
    )
    assert resp.status_code == 401


@pytest.mark.asyncio
async def test_missing_admin_token_rejected(apps_client: AsyncClient) -> None:
    resp = await apps_client.post(
        "/v1/apps/insight/roles/admin/members",
        json={"tenant_id": TENANT, "subject": SUBJECT},
        headers=TENANT_HEADER,
    )
    assert resp.status_code in (401, 403)


@pytest.mark.asyncio
async def test_hyphen_and_dot_encoding(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    resp = await apps_client.post(
        "/v1/apps/my-app/roles/power.user/permissions",
        json={"tenant_id": TENANT, "permission": "data.write"},
        headers=HEADERS,
    )
    assert resp.status_code == 200
    mock_spicedb_client.grant_permission.assert_awaited_once_with(
        tenant_id=TENANT,
        resource="app_permission:my_dash_app|data_dot_write_tenant_a",
        relation="granted_to",
        subject="app_role:my_dash_app|power_dot_user_tenant_a",
    )


@pytest.mark.asyncio
async def test_app_name_rejects_dot_to_match_gateway_host_shape(
    apps_client: AsyncClient,
) -> None:
    resp = await apps_client.post(
        "/v1/apps/my.app/roles/admin/members",
        json={"tenant_id": TENANT, "subject": SUBJECT},
        headers=HEADERS,
    )
    assert resp.status_code == 422


@pytest.mark.asyncio
async def test_check_can_use_dedicated_check_token(
    apps_client: AsyncClient,
    mock_spicedb_client: AsyncMock,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv("AUTHZ_CHECK_TOKEN", "check-token")
    resp = await apps_client.post(
        "/v1/apps/insight/permissions/check",
        json={"tenant_id": TENANT, "permission": "report.manage", "subject": SUBJECT},
        headers={"authorization": "Bearer check-token", **TENANT_HEADER},
    )
    assert resp.status_code == 200


@pytest.mark.asyncio
async def test_list_roles_for_subject_returns_readable_app_roles(
    apps_client: AsyncClient,
    mock_spicedb_client: AsyncMock,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv("AUTHZ_CHECK_TOKEN", "check-token")
    mock_spicedb_client.lookup_subjects = AsyncMock(
        return_value=LookupSubjectsResponse(
            subjects=[
                "app_role:insight|viewer_tenant_a",
                "app_role:insight|power_dot_user_tenant_a",
                "app_role:analytics|admin_tenant_a",
                "app_role:insight|admin_tenant_b",
            ],
            looked_up_at=ZEDTOKEN_READ,
        )
    )

    resp = await apps_client.post(
        "/v1/apps/insight/roles/list-for-subject",
        json={"tenant_id": TENANT, "subject": SUBJECT},
        headers={"authorization": "Bearer check-token", **TENANT_HEADER},
    )

    assert resp.status_code == 200
    assert resp.json() == {"roles": ["power.user", "viewer"]}
    mock_spicedb_client.lookup_subjects.assert_awaited_once_with(
        tenant_id=TENANT,
        resource=ASSIGNMENT_OBJ,
        permission="assigned",
        subject_object_type="app_role",
    )


@pytest.mark.asyncio
async def test_list_roles_for_subject_rejects_wrong_check_token(
    apps_client: AsyncClient,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv("AUTHZ_CHECK_TOKEN", "check-token")
    resp = await apps_client.post(
        "/v1/apps/insight/roles/list-for-subject",
        json={"tenant_id": TENANT, "subject": SUBJECT},
        headers={"authorization": "Bearer wrong-token", **TENANT_HEADER},
    )
    assert resp.status_code == 403


def test_schema_defines_app_role_and_app_permission() -> None:
    schema = pathlib.Path(__file__).resolve().parents[1] / "schema" / "cortex.zed"
    text = schema.read_text()
    assert "definition app_role" in text
    assert "definition app_role_assignment" in text
    assert "definition app_permission" in text
    assert "permission check = granted_to->member" in text
    application = text.split("definition application {", 1)[1].split("}\n", 1)[0]
    assert "relation accessor: user | bootstrap" in application
    assert "permission can_access = bootstrap_access + role->member" in application
    assert "permission can_access = accessor" not in application


SENTINEL_PERM_OBJ = (
    "app_permission:insight|cortex_dot_role_dot_defined_tenant_a"
)


@pytest.mark.asyncio
async def test_ensure_role_writes_sentinel_grant(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    resp = await apps_client.put(
        "/v1/apps/insight/roles/admin",
        json={"tenant_id": TENANT},
        headers=HEADERS,
    )
    assert resp.status_code == 200
    assert resp.json()["granted_at"] == ZEDTOKEN_WRITE
    # ensure_role now writes both edges atomically in a single grant_permissions call:
    # 1. the sentinel permission so the role is enumerable
    # 2. application#role@app_role so gateway can_access resolves via role->member
    # Both succeed or fail together — no partial-write window.
    mock_spicedb_client.grant_permissions.assert_awaited_once_with(
        tenant_id=TENANT,
        relationships=[
            {
                "resource": SENTINEL_PERM_OBJ,
                "relation": "granted_to",
                "subject": ROLE_OBJ,
            },
            {
                "resource": "application:insight@tenant-a",
                "relation": "role",
                "subject": ROLE_OBJ,
            },
        ],
    )
    # grant_permission should not have been called for the role-ensure path
    mock_spicedb_client.grant_permission.assert_not_called()


@pytest.mark.asyncio
async def test_ensure_role_requires_admin_token(apps_client: AsyncClient) -> None:
    resp = await apps_client.put(
        "/v1/apps/insight/roles/admin",
        json={"tenant_id": TENANT},
        headers={"authorization": "Bearer wrong", **TENANT_HEADER},
    )
    assert resp.status_code == 403


@pytest.mark.asyncio
async def test_list_roles_enumerates_via_sentinel(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    mock_spicedb_client.list_relationships = AsyncMock(
        return_value=ListRelationshipsResponse(
            relationships=[
                RelationshipItem(
                    resource=SENTINEL_PERM_OBJ,
                    relation="granted_to",
                    subject="app_role:insight|viewer_tenant_a",
                ),
                RelationshipItem(
                    resource=SENTINEL_PERM_OBJ,
                    relation="granted_to",
                    subject="app_role:insight|admin_tenant_a",
                ),
            ],
            read_at=ZEDTOKEN_READ,
        )
    )

    resp = await apps_client.get("/v1/apps/insight/roles", headers=HEADERS)

    assert resp.status_code == 200
    assert resp.json() == {"roles": ["admin", "viewer"]}
    mock_spicedb_client.list_relationships.assert_awaited_once_with(
        tenant_id=TENANT,
        resource_type="app_permission",
        resource_id="insight|cortex_dot_role_dot_defined_tenant_a",
        relation="granted_to",
    )


@pytest.mark.asyncio
async def test_list_roles_filters_other_apps_and_tenants(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    mock_spicedb_client.list_relationships = AsyncMock(
        return_value=ListRelationshipsResponse(
            relationships=[
                RelationshipItem(
                    resource=SENTINEL_PERM_OBJ,
                    relation="granted_to",
                    subject="app_role:insight|viewer_tenant_a",
                ),
                RelationshipItem(
                    resource=SENTINEL_PERM_OBJ,
                    relation="granted_to",
                    subject="app_role:analytics|admin_tenant_a",
                ),
                RelationshipItem(
                    resource=SENTINEL_PERM_OBJ,
                    relation="granted_to",
                    subject="app_role:insight|admin_tenant_b",
                ),
            ],
            read_at=ZEDTOKEN_READ,
        )
    )

    resp = await apps_client.get("/v1/apps/insight/roles", headers=HEADERS)

    assert resp.status_code == 200
    assert resp.json() == {"roles": ["viewer"]}


@pytest.mark.asyncio
async def test_list_roles_empty(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    mock_spicedb_client.list_relationships = AsyncMock(
        return_value=ListRelationshipsResponse(
            relationships=[], read_at=ZEDTOKEN_READ
        )
    )
    resp = await apps_client.get("/v1/apps/insight/roles", headers=HEADERS)
    assert resp.status_code == 200
    assert resp.json() == {"roles": []}


@pytest.mark.asyncio
async def test_list_roles_requires_admin_token(apps_client: AsyncClient) -> None:
    resp = await apps_client.get(
        "/v1/apps/insight/roles",
        headers={"authorization": "Bearer wrong", **TENANT_HEADER},
    )
    assert resp.status_code == 403


@pytest.mark.asyncio
async def test_list_roles_requires_tenant_header(apps_client: AsyncClient) -> None:
    resp = await apps_client.get("/v1/apps/insight/roles", headers=AUTH)
    assert resp.status_code == 401


@pytest.mark.asyncio
async def test_list_role_members_decodes_subjects(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    mock_spicedb_client.list_relationships = AsyncMock(
        return_value=ListRelationshipsResponse(
            relationships=[
                RelationshipItem(
                    resource=ROLE_OBJ,
                    relation="member",
                    subject="user:alice_at_example_dot_com",
                ),
                RelationshipItem(
                    resource=ROLE_OBJ,
                    relation="member",
                    subject="user:bob_at_example_dot_com",
                ),
            ],
            read_at=ZEDTOKEN_READ,
        )
    )

    resp = await apps_client.get(
        "/v1/apps/insight/roles/admin/members", headers=HEADERS
    )

    assert resp.status_code == 200
    assert resp.json() == {
        "members": ["user:alice@example.com", "user:bob@example.com"]
    }
    mock_spicedb_client.list_relationships.assert_awaited_once_with(
        tenant_id=TENANT,
        resource_type="app_role",
        resource_id="insight|admin_tenant_a",
        relation="member",
    )


@pytest.mark.asyncio
async def test_list_role_members_requires_admin_token(
    apps_client: AsyncClient,
) -> None:
    resp = await apps_client.get(
        "/v1/apps/insight/roles/admin/members",
        headers={"authorization": "Bearer wrong", **TENANT_HEADER},
    )
    assert resp.status_code == 403


@pytest.mark.asyncio
async def test_list_apps_enumerates_apps_with_roles(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    mock_spicedb_client.list_relationships = AsyncMock(
        return_value=ListRelationshipsResponse(
            relationships=[
                # sentinels for two apps in this tenant
                RelationshipItem(
                    resource="app_permission:insight|cortex_dot_role_dot_defined_tenant_a",
                    relation="granted_to",
                    subject="app_role:insight|admin_tenant_a",
                ),
                RelationshipItem(
                    resource="app_permission:analytics|cortex_dot_role_dot_defined_tenant_a",
                    relation="granted_to",
                    subject="app_role:analytics|viewer_tenant_a",
                ),
                # a real (non-sentinel) permission — must NOT count as an app
                RelationshipItem(
                    resource="app_permission:insight|report_dot_manage_tenant_a",
                    relation="granted_to",
                    subject="app_role:insight|admin_tenant_a",
                ),
                # another tenant's sentinel — must be filtered out
                RelationshipItem(
                    resource="app_permission:insight|cortex_dot_role_dot_defined_tenant_b",
                    relation="granted_to",
                    subject="app_role:insight|admin_tenant_b",
                ),
            ],
            read_at=ZEDTOKEN_READ,
        )
    )

    resp = await apps_client.get("/v1/apps", headers=HEADERS)
    assert resp.status_code == 200
    assert resp.json() == {"apps": ["analytics", "insight"]}
    mock_spicedb_client.list_relationships.assert_awaited_once_with(
        tenant_id=TENANT,
        resource_type="app_permission",
        relation="granted_to",
    )


@pytest.mark.asyncio
async def test_list_apps_requires_admin_token(apps_client: AsyncClient) -> None:
    resp = await apps_client.get(
        "/v1/apps", headers={"authorization": "Bearer wrong", **TENANT_HEADER}
    )
    assert resp.status_code == 403


@pytest.mark.asyncio
async def test_list_role_permissions_decodes_and_skips_sentinel(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    mock_spicedb_client.list_relationships = AsyncMock(
        return_value=ListRelationshipsResponse(
            relationships=[
                RelationshipItem(
                    resource="app_permission:insight|dashboard_dot_view_tenant_a",
                    relation="granted_to",
                    subject=ROLE_OBJ,
                ),
                RelationshipItem(
                    resource="app_permission:insight|report_dot_manage_tenant_a",
                    relation="granted_to",
                    subject=ROLE_OBJ,
                ),
                # the sentinel must be excluded from the real permission set
                RelationshipItem(
                    resource=SENTINEL_PERM_OBJ,
                    relation="granted_to",
                    subject=ROLE_OBJ,
                ),
            ],
            read_at=ZEDTOKEN_READ,
        )
    )

    resp = await apps_client.get(
        "/v1/apps/insight/roles/admin/permissions", headers=HEADERS
    )
    assert resp.status_code == 200
    assert resp.json() == {"permissions": ["dashboard.view", "report.manage"]}
    mock_spicedb_client.list_relationships.assert_awaited_once_with(
        tenant_id=TENANT,
        resource_type="app_permission",
        relation="granted_to",
        subject=ROLE_OBJ,
    )


@pytest.mark.asyncio
async def test_list_role_permissions_requires_admin_token(
    apps_client: AsyncClient,
) -> None:
    resp = await apps_client.get(
        "/v1/apps/insight/roles/admin/permissions",
        headers={"authorization": "Bearer wrong", **TENANT_HEADER},
    )
    assert resp.status_code == 403


# ---------------------------------------------------------------------------
# GET /v1/apps/all — every application the tenant has (decoded)
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_list_all_apps_decodes_application_objects(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    mock_spicedb_client.list_relationships = AsyncMock(
        return_value=ListRelationshipsResponse(
            relationships=[
                RelationshipItem(
                    resource="application:example_app_deployment_tenant_a",
                    relation="role",
                    subject="app_role:example_app_deployment|admin_tenant_a",
                ),
                RelationshipItem(
                    resource="application:cortex_tenant_ui_tenant_a",
                    relation="role",
                    subject="app_role:cortex_tenant_ui|admin_tenant_a",
                ),
                # a second role on an already-seen app must not duplicate it
                RelationshipItem(
                    resource="application:example_app_deployment_tenant_a",
                    relation="role",
                    subject="app_role:example_app_deployment|viewer_tenant_a",
                ),
            ],
            read_at=ZEDTOKEN_READ,
        )
    )

    resp = await apps_client.get("/v1/apps/all", headers=HEADERS)

    assert resp.status_code == 200
    assert resp.json() == {"apps": ["cortex-tenant-ui", "example-app-deployment"]}
    mock_spicedb_client.list_relationships.assert_awaited_once_with(
        tenant_id=TENANT,
        resource_type="application",
        relation="role",
    )


@pytest.mark.asyncio
async def test_list_all_apps_filters_other_tenant_suffixes(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    mock_spicedb_client.list_relationships = AsyncMock(
        return_value=ListRelationshipsResponse(
            relationships=[
                RelationshipItem(
                    resource="application:insight_tenant_a",
                    relation="role",
                    subject="app_role:insight|admin_tenant_a",
                ),
                # belongs to a different tenant — must be excluded
                RelationshipItem(
                    resource="application:insight_tenant_b",
                    relation="role",
                    subject="app_role:insight|admin_tenant_b",
                ),
            ],
            read_at=ZEDTOKEN_READ,
        )
    )

    resp = await apps_client.get("/v1/apps/all", headers=HEADERS)

    assert resp.status_code == 200
    assert resp.json() == {"apps": ["insight"]}


@pytest.mark.asyncio
async def test_list_all_apps_empty(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    resp = await apps_client.get("/v1/apps/all", headers=HEADERS)
    assert resp.status_code == 200
    assert resp.json() == {"apps": []}


@pytest.mark.asyncio
async def test_list_all_apps_requires_admin_token(apps_client: AsyncClient) -> None:
    resp = await apps_client.get(
        "/v1/apps/all", headers={"authorization": "Bearer wrong", **TENANT_HEADER}
    )
    assert resp.status_code == 403


@pytest.mark.asyncio
async def test_list_all_apps_requires_tenant_header(apps_client: AsyncClient) -> None:
    resp = await apps_client.get("/v1/apps/all", headers=AUTH)
    assert resp.status_code == 401


# ---------------------------------------------------------------------------
# DELETE /v1/apps/{app} — purge the app's full SpiceDB footprint
# ---------------------------------------------------------------------------


def _purge_list_relationships_side_effect(*, tenant_id, resource_type, **kwargs):
    """Return seeded objects per resource_type for the purge enumeration."""
    rows = {
        "app_role": [
            RelationshipItem(
                resource="app_role:insight|admin_tenant_a",
                relation="member",
                subject=SUBJECT,
            ),
            RelationshipItem(
                resource="app_role:insight|viewer_tenant_a",
                relation="member",
                subject="user:bob@example.com",
            ),
            # different app — must NOT be purged
            RelationshipItem(
                resource="app_role:analytics|admin_tenant_a",
                relation="member",
                subject=SUBJECT,
            ),
            # different tenant — must NOT be purged
            RelationshipItem(
                resource="app_role:insight|admin_tenant_b",
                relation="member",
                subject=SUBJECT,
            ),
        ],
        "app_permission": [
            RelationshipItem(
                resource="app_permission:insight|report_dot_manage_tenant_a",
                relation="granted_to",
                subject="app_role:insight|admin_tenant_a",
            ),
            RelationshipItem(
                resource="app_permission:insight|cortex_dot_role_dot_defined_tenant_a",
                relation="granted_to",
                subject="app_role:insight|admin_tenant_a",
            ),
        ],
        "app_role_assignment": [
            RelationshipItem(
                resource="app_role_assignment:insight|user_colon_alice_at_example_dot_com_tenant_a",
                relation="role",
                subject="app_role:insight|admin_tenant_a",
            ),
        ],
    }
    return ListRelationshipsResponse(
        relationships=rows.get(resource_type, []), read_at=ZEDTOKEN_READ
    )


@pytest.mark.asyncio
async def test_purge_app_deletes_full_footprint_and_accessor(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    mock_spicedb_client.list_relationships = AsyncMock(
        side_effect=_purge_list_relationships_side_effect
    )

    resp = await apps_client.request("DELETE", "/v1/apps/insight", headers=HEADERS)

    assert resp.status_code == 200
    body = resp.json()
    assert body["app"] == "insight"
    assert body["tenant_id"] == TENANT
    assert body["roles_unlinked"] is True
    # 2 app_role + 1 app_permission(real) + 1 sentinel + 1 assignment = 5 objects
    assert body["objects_deleted"] == 5

    deleted = {
        (c.kwargs["resource_type"], c.kwargs["object_id"], c.kwargs.get("relation"))
        for c in mock_spicedb_client.delete_object.await_args_list
    }
    # app-scoped objects for THIS app+tenant, deleted by exact id (no relation filter)
    assert ("app_role", "insight|admin_tenant_a", None) in deleted
    assert ("app_role", "insight|viewer_tenant_a", None) in deleted
    assert ("app_permission", "insight|report_dot_manage_tenant_a", None) in deleted
    assert (
        "app_permission",
        "insight|cortex_dot_role_dot_defined_tenant_a",
        None,
    ) in deleted
    assert (
        "app_role_assignment",
        "insight|user_colon_alice_at_example_dot_com_tenant_a",
        None,
    ) in deleted
    # legacy accessor and bootstrap access edges removed
    assert ("application", "insight_tenant_a", "accessor") in deleted
    assert ("application", "insight_tenant_a", "bootstrap_access") in deleted
    # application#role links removed
    assert ("application", "insight_tenant_a", "role") in deleted
    # other app / other tenant objects were NOT deleted
    assert not any("analytics" in oid for (_, oid, _) in deleted)
    assert not any(oid.endswith("_tenant_b") for (_, oid, _) in deleted)


@pytest.mark.asyncio
async def test_purge_app_is_idempotent_noop_when_nothing_seeded(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    # default mock list_relationships returns empty
    resp = await apps_client.request("DELETE", "/v1/apps/insight", headers=HEADERS)

    assert resp.status_code == 200
    body = resp.json()
    assert body["objects_deleted"] == 0
    assert body["roles_unlinked"] is True
    # still attempts accessor, bootstrap access, and role deletes (harmless no-ops)
    deleted = {
        (c.kwargs["resource_type"], c.kwargs["object_id"], c.kwargs.get("relation"))
        for c in mock_spicedb_client.delete_object.await_args_list
    }
    assert ("application", "insight_tenant_a", "accessor") in deleted
    assert ("application", "insight_tenant_a", "bootstrap_access") in deleted
    assert ("application", "insight_tenant_a", "role") in deleted


@pytest.mark.asyncio
async def test_purge_app_encodes_hyphenated_app_and_tenant(
    apps_client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    # app id with hyphens, default-empty enumeration → only accessor+role deletes,
    # exercising the encoding of both app and tenant in the application object id.
    resp = await apps_client.request(
        "DELETE", "/v1/apps/example-app-deployment", headers=HEADERS
    )
    assert resp.status_code == 200
    deleted = {
        (c.kwargs["resource_type"], c.kwargs["object_id"], c.kwargs.get("relation"))
        for c in mock_spicedb_client.delete_object.await_args_list
    }
    assert ("application", "example_app_deployment_tenant_a", "accessor") in deleted
    assert (
        "application",
        "example_app_deployment_tenant_a",
        "bootstrap_access",
    ) in deleted
    assert ("application", "example_app_deployment_tenant_a", "role") in deleted


@pytest.mark.asyncio
async def test_purge_app_requires_admin_token(apps_client: AsyncClient) -> None:
    resp = await apps_client.request(
        "DELETE",
        "/v1/apps/insight",
        headers={"authorization": "Bearer wrong", **TENANT_HEADER},
    )
    assert resp.status_code == 403


@pytest.mark.asyncio
async def test_purge_app_requires_tenant_header(apps_client: AsyncClient) -> None:
    resp = await apps_client.request("DELETE", "/v1/apps/insight", headers=AUTH)
    assert resp.status_code == 401
