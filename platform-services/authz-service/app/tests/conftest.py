"""Shared fixtures for authz-service tests.

Provides:
- Mock SpiceDB client that satisfies the AuthorizationClient protocol
- FastAPI test client wired with the mock
- Canonical test data for tenants, users, and resources
"""

from __future__ import annotations

import base64
import json
import os
import time
from unittest.mock import AsyncMock

import pytest
import pytest_asyncio
from httpx import ASGITransport, AsyncClient

from data.models import (
    CheckPermissionResponse,
    GrantPermissionResponse,
    ListRelationshipsResponse,
    LookupSubjectsResponse,
    RelationshipItem,
    RevokePermissionResponse,
)

# ---------------------------------------------------------------------------
# Canonical test-data constants
# ---------------------------------------------------------------------------

TENANT_A = "tenant-alpha"
TENANT_B = "tenant-beta"

USER_OWNER_A = "user:owner-alice"
USER_MEMBER_A = "user:member-bob"
USER_OWNER_B = "user:owner-carol"
USER_MEMBER_B = "user:member-dave"

AGENT_RESOURCE_A = f"agent:agent-001@{TENANT_A}"
APPLICATION_RESOURCE_A = f"application:app-001@{TENANT_A}"
AGENT_RESOURCE_B = f"agent:agent-002@{TENANT_B}"

ZEDTOKEN_READ = "zedtoken-snap-read-01"
ZEDTOKEN_WRITE = "zedtoken-snap-write-01"

TEST_ROUTING_DOMAIN = "cortex.ai"
os.environ.setdefault("ROUTING_DOMAIN", TEST_ROUTING_DOMAIN)

# This deployment serves exactly one tenant, so the suite runs AS tenant-alpha.
# TENANT_B is retained deliberately: it is no longer "another tenant whose data
# must stay separate" but "a tenant this deployment does not serve", and every
# request naming it must be refused rather than quietly served.
os.environ.setdefault("AUTHZ_TENANT_ID", TENANT_A)

# ---------------------------------------------------------------------------
# SpiceDB mock
# ---------------------------------------------------------------------------


@pytest.fixture
def mock_spicedb_client() -> AsyncMock:
    """AsyncMock that satisfies the AuthorizationClient protocol.

    Default behaviour:
      - check_permission → allowed=True
      - grant_permission → GrantPermissionResponse with a stable token
      - revoke_permission → RevokePermissionResponse with a stable token

    Individual tests may override the return_value or side_effect.
    """
    client = AsyncMock()
    client.check_permission = AsyncMock(
        return_value=CheckPermissionResponse(
            allowed=True,
            checked_at=ZEDTOKEN_READ,
        )
    )
    client.grant_permission = AsyncMock(
        return_value=GrantPermissionResponse(granted_at=ZEDTOKEN_WRITE)
    )
    client.grant_permissions = AsyncMock(
        return_value=GrantPermissionResponse(granted_at=ZEDTOKEN_WRITE)
    )
    client.revoke_permission = AsyncMock(
        return_value=RevokePermissionResponse(revoked_at=ZEDTOKEN_WRITE)
    )
    client.revoke_permissions = AsyncMock(
        return_value=RevokePermissionResponse(revoked_at=ZEDTOKEN_WRITE)
    )
    client.delete_object = AsyncMock(
        return_value=RevokePermissionResponse(revoked_at=ZEDTOKEN_WRITE)
    )
    client.list_relationships = AsyncMock(
        return_value=ListRelationshipsResponse(
            relationships=[
                RelationshipItem(
                    resource="app_permission:sentinel",
                    relation="granted_to",
                    subject="app_role:seeded",
                )
            ],
            read_at=ZEDTOKEN_READ,
        )
    )
    client.lookup_subjects = AsyncMock(
        return_value=LookupSubjectsResponse(subjects=[], looked_up_at=ZEDTOKEN_READ)
    )
    return client


# ---------------------------------------------------------------------------
# Application factory and test client
# ---------------------------------------------------------------------------


@pytest_asyncio.fixture
async def client(mock_spicedb_client: AsyncMock) -> AsyncClient:
    """Async HTTP test client with the SpiceDB client dependency overridden."""
    from app.main import build_app
    from common.dependencies import get_authz_client

    app = build_app()
    app.dependency_overrides[get_authz_client] = lambda: mock_spicedb_client

    async with AsyncClient(
        transport=ASGITransport(app=app), base_url="http://test"
    ) as ac:
        yield ac


# ---------------------------------------------------------------------------
# Bearer token helpers
#
# Envoy's OIDC policy is the authentication gate; ext_authz only sees Bearer
# tokens. All test helpers produce a minimal unsigned JWT (signature is not
# verified in tests — decode_access_token only base64-decodes the payload).
# ---------------------------------------------------------------------------


def _make_jwt(claims: dict) -> str:
    """Build a minimal unsigned JWT from a claims dict."""
    def b64(data: bytes) -> str:
        return base64.urlsafe_b64encode(data).rstrip(b"=").decode()

    header = b64(json.dumps({"alg": "none", "typ": "JWT"}).encode())
    payload = b64(json.dumps(claims).encode())
    return f"{header}.{payload}."


def bearer_headers(
    sub: str = USER_OWNER_A,
    tenant_id: str = TENANT_A,
    host: str | None = None,
    ttl: int = 3600,
) -> dict[str, str]:
    """Return headers dict with a Bearer token JWT and optional host."""
    now = int(time.time())
    # Strip the "user:" prefix for the JWT sub claim — token_decoder adds it back.
    raw_sub = sub.removeprefix("user:")
    claims = {
        "sub": raw_sub,
        "preferred_username": raw_sub,
        "tid": tenant_id,
        "iat": now,
        "exp": now + ttl,
    }
    hdrs: dict[str, str] = {"authorization": f"Bearer {_make_jwt(claims)}"}
    if host:
        hdrs["host"] = host
    return hdrs


def owner_headers(
    tenant_id: str = TENANT_A,
    sub: str = USER_OWNER_A,
    host: str | None = None,
) -> dict[str, str]:
    return bearer_headers(sub=sub, tenant_id=tenant_id, host=host)


def member_headers(
    tenant_id: str = TENANT_A,
    sub: str = USER_MEMBER_A,
    host: str | None = None,
) -> dict[str, str]:
    return bearer_headers(sub=sub, tenant_id=tenant_id, host=host)
