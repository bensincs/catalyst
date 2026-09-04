from __future__ import annotations

from unittest.mock import AsyncMock

import pytest
import pytest_asyncio
from httpx import ASGITransport, AsyncClient

from tests.conftest import TENANT_A, ZEDTOKEN_WRITE

ADMIN_TOKEN = "test-admin-token"
AUTH_HEADER = {"authorization": f"Bearer {ADMIN_TOKEN}"}


@pytest_asyncio.fixture
async def admin_client(mock_spicedb_client: AsyncMock, monkeypatch) -> AsyncClient:
    monkeypatch.setenv("AUTHZ_ADMIN_TOKEN", ADMIN_TOKEN)
    from app.main import build_app
    from common.dependencies import get_authz_client

    app = build_app()
    app.dependency_overrides[get_authz_client] = lambda: mock_spicedb_client
    async with AsyncClient(transport=ASGITransport(app=app), base_url="http://test") as ac:
        yield ac


@pytest.mark.asyncio
async def test_write_relationships_success(admin_client, mock_spicedb_client):
    payload = {
        "tenant_id": TENANT_A,
        "relationships": [
            {
                "resource": f"application:cortex_tenant_ui@{TENANT_A}",
                "relation": "role",
                "subject": "app_role:cortex_tenant_ui|admin_tenant_alpha",
            },
            {
                "resource": f"application:cortex_auth_ui@{TENANT_A}",
                "relation": "role",
                "subject": "app_role:cortex_auth_ui|admin_tenant_alpha",
            },
        ],
    }
    response = await admin_client.post("/v1/admin/relationships", json=payload, headers=AUTH_HEADER)
    assert response.status_code == 200
    assert response.json()["written_at"] == ZEDTOKEN_WRITE
    assert mock_spicedb_client.grant_permission.call_count == 2


@pytest.mark.asyncio
async def test_write_relationships_no_token(admin_client):
    payload = {
        "tenant_id": TENANT_A,
        "relationships": [
            {"resource": "application:foo@t", "relation": "role", "subject": "app_role:foo|admin_t"}
        ],
    }
    response = await admin_client.post("/v1/admin/relationships", json=payload)
    assert response.status_code == 403 or response.status_code == 401


@pytest.mark.asyncio
async def test_write_relationships_wrong_token(admin_client):
    payload = {
        "tenant_id": TENANT_A,
        "relationships": [
            {"resource": "application:foo@t", "relation": "role", "subject": "app_role:foo|admin_t"}
        ],
    }
    response = await admin_client.post(
        "/v1/admin/relationships", json=payload, headers={"authorization": "Bearer wrong"}
    )
    assert response.status_code == 403


@pytest.mark.asyncio
async def test_write_relationships_unconfigured_token(mock_spicedb_client, monkeypatch):
    monkeypatch.delenv("AUTHZ_ADMIN_TOKEN", raising=False)
    from app.main import build_app
    from common.dependencies import get_authz_client

    app = build_app()
    app.dependency_overrides[get_authz_client] = lambda: mock_spicedb_client
    async with AsyncClient(transport=ASGITransport(app=app), base_url="http://test") as ac:
        payload = {
            "tenant_id": TENANT_A,
            "relationships": [
                {"resource": "application:foo@t", "relation": "role", "subject": "app_role:foo|admin_t"}
            ],
        }
        response = await ac.post(
            "/v1/admin/relationships",
            json=payload,
            headers={"authorization": "Bearer anything"},
        )
        assert response.status_code == 503


# ---------------------------------------------------------------------------
# GET /v1/admin/relationships — tenant-scoped list with decoded fields
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_list_relationships_decodes_application_and_subject(
    admin_client, mock_spicedb_client
):
    from data.models import ListRelationshipsResponse, RelationshipItem

    mock_spicedb_client.list_relationships = AsyncMock(
        return_value=ListRelationshipsResponse(
            relationships=[
                RelationshipItem(
                    resource="application:example_app_deployment_tenant_alpha",
                    relation="accessor",
                    subject="user:ben_dot_sinclair_at_example_dot_com",
                ),
            ],
            read_at=ZEDTOKEN_WRITE,
        )
    )

    resp = await admin_client.get(
        "/v1/admin/relationships",
        params={"tenant_id": TENANT_A, "resource_type": "application"},
        headers=AUTH_HEADER,
    )

    assert resp.status_code == 200
    body = resp.json()
    assert len(body["relationships"]) == 1
    item = body["relationships"][0]
    # raw fields preserved
    assert item["resource"] == "application:example_app_deployment_tenant_alpha"
    assert item["subject"] == "user:ben_dot_sinclair_at_example_dot_com"
    # decoded fields recover the originals (this is the accessor-delete fix)
    assert item["application"] == "example-app-deployment"
    assert item["subject_id"] == "ben.sinclair@example.com"


@pytest.mark.asyncio
async def test_list_relationships_scopes_to_tenant(admin_client, mock_spicedb_client):
    from data.models import ListRelationshipsResponse, RelationshipItem

    mock_spicedb_client.list_relationships = AsyncMock(
        return_value=ListRelationshipsResponse(
            relationships=[
                RelationshipItem(
                    resource="application:insight_tenant_alpha",
                    relation="accessor",
                    subject="user:alice_at_example_dot_com",
                ),
                # different tenant suffix — must be filtered out
                RelationshipItem(
                    resource="application:insight_tenant_beta",
                    relation="accessor",
                    subject="user:dave_at_example_dot_com",
                ),
            ],
            read_at=ZEDTOKEN_WRITE,
        )
    )

    resp = await admin_client.get(
        "/v1/admin/relationships",
        params={"tenant_id": TENANT_A},
        headers=AUTH_HEADER,
    )

    assert resp.status_code == 200
    apps = [r["application"] for r in resp.json()["relationships"]]
    assert apps == ["insight"]


@pytest.mark.asyncio
async def test_list_relationships_non_application_resource_has_null_application(
    admin_client, mock_spicedb_client
):
    from data.models import ListRelationshipsResponse, RelationshipItem

    mock_spicedb_client.list_relationships = AsyncMock(
        return_value=ListRelationshipsResponse(
            relationships=[
                RelationshipItem(
                    resource="agent:my_agent_tenant_alpha",
                    relation="accessor",
                    subject="user:alice_at_example_dot_com",
                ),
            ],
            read_at=ZEDTOKEN_WRITE,
        )
    )

    resp = await admin_client.get(
        "/v1/admin/relationships",
        params={"tenant_id": TENANT_A, "resource_type": "agent"},
        headers=AUTH_HEADER,
    )

    assert resp.status_code == 200
    item = resp.json()["relationships"][0]
    # only application resources get a decoded application id; agents do not
    assert item["application"] is None
    assert item["subject_id"] == "alice@example.com"


@pytest.mark.asyncio
async def test_list_relationships_requires_admin_token(admin_client):
    resp = await admin_client.get(
        "/v1/admin/relationships",
        params={"tenant_id": TENANT_A},
        headers={"authorization": "Bearer wrong"},
    )
    assert resp.status_code == 403


@pytest.mark.asyncio
async def test_list_subjects_reads_user_role_members(admin_client, mock_spicedb_client):
    from data.models import ListRelationshipsResponse, RelationshipItem

    mock_spicedb_client.list_relationships = AsyncMock(
        return_value=ListRelationshipsResponse(
            relationships=[
                RelationshipItem(
                    resource="app_role:insight|admin_tenant_alpha",
                    relation="member",
                    subject="user:alice_at_example_dot_com",
                ),
                RelationshipItem(
                    resource="app_role:insight|viewer_tenant_alpha",
                    relation="member",
                    subject="service:ignored",
                ),
            ],
            read_at=ZEDTOKEN_WRITE,
        )
    )

    response = await admin_client.get(
        "/v1/admin/subjects",
        params={"tenant_id": TENANT_A},
        headers=AUTH_HEADER,
    )

    assert response.status_code == 200
    assert response.json() == {"subjects": ["alice_at_example_dot_com"]}
    mock_spicedb_client.list_relationships.assert_awaited_once_with(
        tenant_id=TENANT_A,
        resource_type="app_role",
        relation="member",
    )
