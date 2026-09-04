"""The authorization decision an identity-aware proxy asks for.

Distinct from the ext_authz routes: those recover the app from the Host header,
which only works when the ORIGINAL request is handed to the authorization
service. A proxy such as Oathkeeper calls with a request of its own, so the app
and subject are given explicitly.
"""

from __future__ import annotations

from unittest.mock import AsyncMock

import pytest
from httpx import AsyncClient

from tests.conftest import TENANT_A

_URL = "/v1/authz/decide"


@pytest.mark.asyncio
async def test_allows_when_spicedb_allows(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    resp = await client.post(_URL, json={"subject": "user:alice@example.com", "app": "insight"})

    assert resp.status_code == 200
    assert resp.json() == {"allowed": True}


@pytest.mark.asyncio
async def test_denies_when_spicedb_denies(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    mock_spicedb_client.check_permission = AsyncMock(
        return_value=type("R", (), {"allowed": False})()
    )

    resp = await client.post(_URL, json={"subject": "user:mallory@example.com", "app": "insight"})

    assert resp.status_code == 403
    assert resp.json() == {"allowed": False}


@pytest.mark.asyncio
async def test_checks_the_app_it_was_given_not_the_host_header(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    """The app comes from the body, so a misleading Host cannot redirect it.

    The proxy's own Host names the authorization service. If this route read it
    the way ext_authz does, every decision would be made against the wrong
    application — or refused outright.
    """
    await client.post(
        _URL,
        json={"subject": "user:alice@example.com", "app": "insight"},
        headers={"host": "authz-service.cortex-authz.svc.cluster.local:8080"},
    )

    kwargs = mock_spicedb_client.check_permission.await_args.kwargs
    assert kwargs["resource"].startswith("application:insight@")
    assert kwargs["permission"] == "can_access"
    assert kwargs["tenant_id"] == TENANT_A


@pytest.mark.asyncio
async def test_bare_subject_is_treated_as_a_user(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    await client.post(_URL, json={"subject": "alice@example.com", "app": "insight"})

    assert mock_spicedb_client.check_permission.await_args.kwargs["subject"] == (
        "user:alice@example.com"
    )


@pytest.mark.asyncio
async def test_fails_closed_when_spicedb_is_unavailable(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    """An authorization service that cannot reach its datastore knows nothing.

    Answering "allow" would hand out access precisely when the system is least
    able to account for it.
    """
    from data.spicedb_client import SpiceDBUnavailableError

    mock_spicedb_client.check_permission = AsyncMock(side_effect=SpiceDBUnavailableError())

    resp = await client.post(_URL, json={"subject": "user:alice@example.com", "app": "insight"})

    assert resp.status_code == 503
    assert resp.json() == {"allowed": False}


@pytest.mark.asyncio
async def test_empty_app_is_refused(client: AsyncClient) -> None:
    resp = await client.post(_URL, json={"subject": "user:alice@example.com", "app": "  "})
    assert resp.status_code == 403
