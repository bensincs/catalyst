"""The API the admin UI calls.

Authorised on the gateway-injected identity rather than a shared token: a token
shipped to a browser is a token given away. Being able to REACH this service is
not permission to change who can reach anything else, so every endpoint
confirms the caller holds the administrator role first.
"""

from __future__ import annotations

from unittest.mock import AsyncMock

import pytest
from httpx import AsyncClient

_ADMIN = {"x-cortex-sub": "user:admin@example.com"}


@pytest.mark.asyncio
async def test_rejects_a_request_with_no_gateway_identity(client: AsyncClient) -> None:
    """No identity header means the request did not come through the gateway."""
    resp = await client.get("/v1/ui/apps")
    assert resp.status_code == 401


@pytest.mark.asyncio
async def test_rejects_a_caller_who_is_not_an_administrator(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    """Reaching the service is not permission to change access.

    Without this check every user of every hosted app could grant themselves
    access to all of them.
    """
    mock_spicedb_client.check_permission = AsyncMock(
        return_value=type("R", (), {"allowed": False})()
    )
    resp = await client.get("/v1/ui/apps", headers={"x-cortex-sub": "user:nobody@example.com"})
    assert resp.status_code == 403


@pytest.mark.asyncio
async def test_administrator_is_confirmed_against_the_admin_app(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    resp = await client.get("/v1/ui/me", headers=_ADMIN)

    assert resp.status_code == 200
    assert resp.json()["administrator"] is True
    kwargs = mock_spicedb_client.check_permission.await_args.kwargs
    assert kwargs["resource"].startswith("application:authz-admin@")
    assert kwargs["subject"] == "user:admin@example.com"


@pytest.mark.asyncio
async def test_grant_records_who_granted_and_to_whom(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    resp = await client.post(
        "/v1/ui/grant",
        json={"app": "insight", "role": "user", "subject": "ben@msft.ae"},
        headers=_ADMIN,
    )

    assert resp.status_code == 200
    assert resp.json()["subject"] == "user:ben@msft.ae"
    rels = mock_spicedb_client.grant_permissions.await_args.kwargs["relationships"]
    assert any(r["subject"] == "user:ben@msft.ae" for r in rels)


@pytest.mark.asyncio
async def test_a_bare_address_is_treated_as_a_user(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    await client.post(
        "/v1/ui/grant",
        json={"app": "insight", "subject": "ben@msft.ae"},
        headers=_ADMIN,
    )
    rels = mock_spicedb_client.grant_permissions.await_args.kwargs["relationships"]
    assert rels[0]["subject"] == "user:ben@msft.ae"


@pytest.mark.asyncio
async def test_revoke_removes_both_edges(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    resp = await client.post(
        "/v1/ui/revoke",
        json={"app": "insight", "role": "user", "subject": "ben@msft.ae"},
        headers=_ADMIN,
    )
    assert resp.status_code == 200
    rels = mock_spicedb_client.revoke_permissions.await_args.kwargs["relationships"]
    assert len(rels) == 2


@pytest.mark.asyncio
async def test_access_lists_every_grant_in_one_query(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    """The table shows people, not one application at a time.

    Reading it per app would be a call per app per role; the role object encodes
    both, so one listing of every member edge answers the whole question.
    """
    from tests.conftest import TENANT_A

    sfx = TENANT_A.replace("-", "_")
    rel = lambda res, sub: type("R", (), {"resource": res, "subject": sub})()
    mock_spicedb_client.list_relationships = AsyncMock(
        return_value=type("L", (), {"relationships": [
            rel(f"app_role:insight|admin_{sfx}", "user:zoe_at_example_dot_com"),
            rel(f"app_role:todo|user_{sfx}", "user:alice_at_example_dot_com"),
        ]})()
    )

    resp = await client.get("/v1/ui/access", headers=_ADMIN)

    assert resp.status_code == 200
    assert mock_spicedb_client.list_relationships.await_count == 1
    grants = resp.json()["grants"]
    assert {(g["subject"], g["app"], g["role"]) for g in grants} == {
        ("user:zoe@example.com", "insight", "admin"),
        ("user:alice@example.com", "todo", "user"),
    }
    # Sorted, so the table does not reshuffle between refreshes.
    assert grants == sorted(grants, key=lambda g: (g["subject"], g["app"], g["role"]))


@pytest.mark.asyncio
async def test_catalog_returns_roles_per_application(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    """Roles belong to their application.

    An earlier version read the roles of the FIRST app and applied that name to
    every app the operator picked, which is wrong the moment two applications
    define different roles.
    """
    from tests.conftest import TENANT_A

    sfx = TENANT_A.replace("-", "_")
    rel = lambda res, sub: type("R", (), {"resource": res, "subject": sub})()
    mock_spicedb_client.list_relationships = AsyncMock(
        return_value=type("L", (), {"relationships": [
            rel(f"app_permission:insight|cortex_dot_role_dot_defined_{sfx}",
                f"app_role:insight|admin_{sfx}"),
            rel(f"app_permission:insight|cortex_dot_role_dot_defined_{sfx}",
                f"app_role:insight|user_{sfx}"),
            rel(f"app_permission:todo|cortex_dot_role_dot_defined_{sfx}",
                f"app_role:todo|user_{sfx}"),
            # Not the sentinel: says what a role can do, not that it exists.
            rel(f"app_permission:todo|report_dot_read_{sfx}", f"app_role:todo|user_{sfx}"),
        ]})()
    )

    resp = await client.get("/v1/ui/catalog", headers=_ADMIN)

    assert resp.status_code == 200
    assert mock_spicedb_client.list_relationships.await_count == 1
    assert resp.json()["apps"] == [
        {"name": "insight", "roles": ["admin", "user"]},
        {"name": "todo", "roles": ["user"]},
    ]
