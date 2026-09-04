"""Tests for /v1/admin/relationships endpoints.

Covers:
- POST /v1/admin/relationships — write (existing)
- GET  /v1/admin/relationships — list
- DELETE /v1/admin/relationships — revoke
"""

from __future__ import annotations

from unittest.mock import AsyncMock

import pytest

from data.models import (
    GrantPermissionResponse,
    ListRelationshipsResponse,
    RelationshipItem,
    RevokePermissionResponse,
)
from tests.conftest import TENANT_A, ZEDTOKEN_READ, ZEDTOKEN_WRITE

ADMIN_TOKEN = "test-admin-token"
AUTH_HEADER = {"Authorization": f"Bearer {ADMIN_TOKEN}"}


@pytest.fixture(autouse=True)
def _set_admin_token(monkeypatch):
    monkeypatch.setenv("AUTHZ_ADMIN_TOKEN", ADMIN_TOKEN)


# ---------------------------------------------------------------------------
# POST /v1/admin/relationships
# ---------------------------------------------------------------------------


class TestWriteRelationships:
    @pytest.mark.asyncio
    async def test_write_succeeds(self, client, mock_spicedb_client) -> None:
        mock_spicedb_client.grant_permission = AsyncMock(
            return_value=GrantPermissionResponse(granted_at=ZEDTOKEN_WRITE)
        )
        resp = await client.post(
            "/v1/admin/relationships",
            json={
                "tenant_id": TENANT_A,
                "relationships": [
                    {
                        "resource": f"application:cortex-tenant-ui@{TENANT_A}",
                        "relation": "accessor",
                        "subject": "user:alice@example.com",
                    }
                ],
            },
            headers=AUTH_HEADER,
        )
        assert resp.status_code == 200
        assert resp.json()["written_at"] == ZEDTOKEN_WRITE

    @pytest.mark.asyncio
    async def test_write_rejects_missing_token(self, client) -> None:
        resp = await client.post(
            "/v1/admin/relationships",
            json={"tenant_id": TENANT_A, "relationships": []},
        )
        assert resp.status_code in (401, 403)

    @pytest.mark.asyncio
    async def test_write_rejects_wrong_token(self, client) -> None:
        resp = await client.post(
            "/v1/admin/relationships",
            json={"tenant_id": TENANT_A, "relationships": []},
            headers={"Authorization": "Bearer wrong-token"},
        )
        assert resp.status_code == 403


# ---------------------------------------------------------------------------
# GET /v1/admin/relationships
# ---------------------------------------------------------------------------


class TestListRelationships:
    @pytest.mark.asyncio
    async def test_list_returns_tenant_scoped_items(self, client, mock_spicedb_client) -> None:
        tenant_suffix = TENANT_A.replace("-", "_")
        mock_spicedb_client.list_relationships = AsyncMock(
            return_value=ListRelationshipsResponse(
                relationships=[
                    RelationshipItem(
                        resource=f"application:cortex_tenant_ui_{tenant_suffix}",
                        relation="accessor",
                        subject="user:alice_at_example_dot_com",
                    ),
                    # Different tenant — should be filtered out
                    RelationshipItem(
                        resource="application:cortex_tenant_ui_other_tenant",
                        relation="accessor",
                        subject="user:bob_at_example_dot_com",
                    ),
                ],
                read_at=ZEDTOKEN_READ,
            )
        )
        resp = await client.get(
            f"/v1/admin/relationships?tenant_id={TENANT_A}",
            headers=AUTH_HEADER,
        )
        assert resp.status_code == 200
        data = resp.json()
        assert len(data["relationships"]) == 1
        assert data["relationships"][0]["subject"] == "user:alice_at_example_dot_com"
        assert data["read_at"] == ZEDTOKEN_READ

    @pytest.mark.asyncio
    async def test_list_rejects_missing_token(self, client) -> None:
        resp = await client.get(f"/v1/admin/relationships?tenant_id={TENANT_A}")
        assert resp.status_code in (401, 403)

    @pytest.mark.asyncio
    async def test_list_requires_tenant_id(self, client) -> None:
        resp = await client.get("/v1/admin/relationships", headers=AUTH_HEADER)
        assert resp.status_code == 422

    @pytest.mark.asyncio
    async def test_list_empty(self, client, mock_spicedb_client) -> None:
        mock_spicedb_client.list_relationships = AsyncMock(
            return_value=ListRelationshipsResponse(relationships=[], read_at="")
        )
        resp = await client.get(
            f"/v1/admin/relationships?tenant_id={TENANT_A}",
            headers=AUTH_HEADER,
        )
        assert resp.status_code == 200
        assert resp.json()["relationships"] == []


# ---------------------------------------------------------------------------
# DELETE /v1/admin/relationships
# ---------------------------------------------------------------------------


class TestDeleteRelationship:
    @pytest.mark.asyncio
    async def test_delete_succeeds(self, client, mock_spicedb_client) -> None:
        mock_spicedb_client.revoke_permission = AsyncMock(
            return_value=RevokePermissionResponse(revoked_at=ZEDTOKEN_WRITE)
        )
        resp = await client.request(
            "DELETE",
            "/v1/admin/relationships",
            json={
                "tenant_id": TENANT_A,
                "resource": f"application:cortex-tenant-ui@{TENANT_A}",
                "relation": "accessor",
                "subject": "user:alice@example.com",
            },
            headers=AUTH_HEADER,
        )
        assert resp.status_code == 200
        assert resp.json()["revoked_at"] == ZEDTOKEN_WRITE

    @pytest.mark.asyncio
    async def test_delete_rejects_missing_token(self, client) -> None:
        resp = await client.request(
            "DELETE",
            "/v1/admin/relationships",
            json={
                "tenant_id": TENANT_A,
                "resource": f"application:cortex-tenant-ui@{TENANT_A}",
                "relation": "accessor",
                "subject": "user:alice@example.com",
            },
        )
        assert resp.status_code in (401, 403)

    @pytest.mark.asyncio
    async def test_delete_rejects_wrong_token(self, client) -> None:
        resp = await client.request(
            "DELETE",
            "/v1/admin/relationships",
            json={
                "tenant_id": TENANT_A,
                "resource": f"application:cortex-tenant-ui@{TENANT_A}",
                "relation": "accessor",
                "subject": "user:alice@example.com",
            },
            headers={"Authorization": "Bearer wrong"},
        )
        assert resp.status_code == 403
