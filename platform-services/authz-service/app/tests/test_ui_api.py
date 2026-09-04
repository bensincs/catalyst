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

# Identity now comes from a verified token, so the tests stand in for the
# verification rather than sending a header the service would refuse.
_ADMIN = {"authorization": "Bearer test-token"}


@pytest.fixture(autouse=True)
def _verifiable_token(monkeypatch: pytest.MonkeyPatch) -> None:
    from common import gateway_identity

    monkeypatch.setattr(gateway_identity, "configured", lambda: True)
    monkeypatch.setattr(
        gateway_identity,
        "subject_from_token",
        lambda auth: (
            "admin@example.com"
            if auth == "Bearer test-token"
            else (_ for _ in ()).throw(gateway_identity.InvalidToken("bad token"))
        ),
    )


@pytest.mark.asyncio
async def test_rejects_a_request_with_no_token(client: AsyncClient) -> None:
    resp = await client.get("/v1/ui/apps")
    assert resp.status_code == 401


@pytest.mark.asyncio
async def test_a_forged_identity_header_proves_nothing(client: AsyncClient) -> None:
    """This is the hole the token verification exists to close.

    The Service's ClusterIP is reachable without going through the gateway, so
    any pod could send the header the gateway stamps. A plain curl from another
    namespace did exactly that and was told it was an administrator.
    """
    resp = await client.get(
        "/v1/ui/me", headers={"x-cortex-sub": "user:attacker@example.com"}
    )
    assert resp.status_code == 401


@pytest.mark.asyncio
async def test_an_unverifiable_token_is_refused(client: AsyncClient) -> None:
    resp = await client.get("/v1/ui/me", headers={"authorization": "Bearer forged"})
    assert resp.status_code == 401


@pytest.mark.asyncio
async def test_refuses_rather_than_trusting_headers_when_unconfigured(
    client: AsyncClient, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Falling back to headers would reinstate the hole."""
    from common import gateway_identity

    monkeypatch.setattr(gateway_identity, "configured", lambda: False)
    resp = await client.get("/v1/ui/me", headers={"x-cortex-sub": "user:a@b.c"})
    assert resp.status_code == 503


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
    resp = await client.get("/v1/ui/apps", headers=_ADMIN)
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


def _rel(resource: str, subject: str):
    return type("R", (), {"resource": resource, "subject": subject})()


def _admins(*subjects: str) -> AsyncMock:
    """A SpiceDB listing in which `subjects` administer app access.

    The role object is built with the same function the service uses rather
    than written out here: an address is encoded on the way in, so a hand-written
    "authz-admin" never matches the stored "authz_dash_admin" and the listing
    silently looks empty.
    """
    from routes.app_permissions import _role_object
    from tests.conftest import TENANT_A

    admin_object = _role_object("authz-admin", "admin", TENANT_A)
    return AsyncMock(
        return_value=type("L", (), {"relationships": [
            _rel(admin_object, s) for s in subjects
        ]})()
    )


@pytest.mark.asyncio
async def test_the_last_administrator_cannot_be_removed(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    """Removing the only administrator locks everyone out for good.

    The one route that grants the role back requires you to already hold it, so
    there is no way back through the UI — recovery means editing SpiceDB by hand.
    """
    mock_spicedb_client.list_relationships = _admins("user:ben_at_msft_dot_ae")

    resp = await client.post(
        "/v1/ui/revoke",
        json={"app": "authz-admin", "role": "admin", "subject": "ben@msft.ae"},
        headers=_ADMIN,
    )

    assert resp.status_code == 409
    mock_spicedb_client.revoke_permissions.assert_not_awaited()


@pytest.mark.asyncio
async def test_an_administrator_can_be_removed_while_another_remains(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    mock_spicedb_client.list_relationships = _admins(
        "user:ben_at_msft_dot_ae", "user:zoe_at_example_dot_com"
    )

    resp = await client.post(
        "/v1/ui/revoke",
        json={"app": "authz-admin", "role": "admin", "subject": "ben@msft.ae"},
        headers=_ADMIN,
    )

    assert resp.status_code == 200
    mock_spicedb_client.revoke_permissions.assert_awaited()


@pytest.mark.asyncio
async def test_revoking_someone_who_is_not_an_administrator_is_not_the_last_one(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    """Counting members would call this the last administrator and refuse.

    The real administrator still holds the role; the person being removed never
    did. Asking who remains rather than how many distinguishes the two.
    """
    mock_spicedb_client.list_relationships = _admins("user:ben_at_msft_dot_ae")

    resp = await client.post(
        "/v1/ui/revoke",
        json={"app": "authz-admin", "role": "admin", "subject": "stranger@example.com"},
        headers=_ADMIN,
    )

    assert resp.status_code == 200


@pytest.mark.asyncio
async def test_the_guard_does_not_apply_to_ordinary_applications(
    client: AsyncClient, mock_spicedb_client: AsyncMock
) -> None:
    """Insight's last org_admin is not a lockout: access can still be restored."""
    resp = await client.post(
        "/v1/ui/revoke",
        json={"app": "insight", "role": "org_admin", "subject": "ben@msft.ae"},
        headers=_ADMIN,
    )

    assert resp.status_code == 200
