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
