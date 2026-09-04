"""Tests for POST /v1/ext-authz/check — Envoy external authorization endpoint.

Routing model
-------------
The hostname encodes both tenant and application:

  <appname>.<tenant>.cortex.ai  → application:<appname>@<tenant>#can_access

All applications use the same permission. Access is granted per-app via SpiceDB
accessor relations.

Identity source
---------------
Identity comes exclusively from the Authorization: Bearer token. Envoy's OIDC
policy validates the token before the request reaches ext_authz. x-cortex-*
headers are no longer used.

Health bypass
-------------
Paths matching /health or /healthz are always allowed (200) without a token.
"""

from __future__ import annotations

from unittest.mock import AsyncMock as _AsyncMock

import pytest
from httpx import AsyncClient

from data.models import CheckPermissionResponse as _CheckResp

from tests.conftest import (
    TENANT_A,
    TENANT_B,
    USER_MEMBER_A,
    USER_OWNER_A,
    ZEDTOKEN_READ,
    _make_jwt,
    bearer_headers,
    member_headers,
    owner_headers,
)

_EXT_AUTHZ_PATH = "/v1/ext-authz/check"

# Canonical test hostnames
_UI_HOST_A = f"cortex-ui.{TENANT_A}.cortex.ai"
_APP_HOST_A = f"my-app.{TENANT_A}.cortex.ai"
_UI_HOST_B = f"cortex-ui.{TENANT_B}.cortex.ai"
_APP_HOST_B = f"my-app.{TENANT_B}.cortex.ai"


# ---------------------------------------------------------------------------
# TC-1  cortex-ui is just another application
# ---------------------------------------------------------------------------


class TestCortexUIAsApplication:
    """cortex-ui.<tenant>.cortex.ai checks application:cortex-ui@<tenant>#can_access."""

    @pytest.mark.asyncio
    async def test_user_on_cortex_ui_returns_200(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_user_allowed_by_spicedb_returns_200(
        self, client: AsyncClient
    ) -> None:
        """SpiceDB allows (explicit grant) → 200."""
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**member_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_user_denied_by_spicedb_returns_403(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        """SpiceDB denies (no grant) → 403."""
        mock_spicedb_client.check_permission = _AsyncMock(
            return_value=_CheckResp(allowed=False, checked_at=ZEDTOKEN_READ)
        )
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**member_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_cortex_ui_checks_can_access_on_application_resource(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        call = mock_spicedb_client.check_permission.await_args
        assert "application:cortex-ui@" in call.kwargs["resource"]
        assert call.kwargs["permission"] == "can_access"


# ---------------------------------------------------------------------------
# TC-2  Application hostname checks can_access on the application resource
# ---------------------------------------------------------------------------


class TestApplicationHostname:
    """<appname>.<tenant>.cortex.ai checks application:<appname>@<tenant>#can_access."""

    @pytest.mark.asyncio
    async def test_user_on_app_returns_200(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _APP_HOST_A},
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_user_with_explicit_grant_returns_200(
        self, client: AsyncClient
    ) -> None:
        """SpiceDB allows → 200."""
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**member_headers(tenant_id=TENANT_A), "host": _APP_HOST_A},
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_user_without_grant_returns_403(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        mock_spicedb_client.check_permission = _AsyncMock(
            return_value=_CheckResp(allowed=False, checked_at=ZEDTOKEN_READ)
        )
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**member_headers(tenant_id=TENANT_A), "host": _APP_HOST_A},
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_app_hostname_checks_can_access_on_application_resource(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        await client.post(
            _EXT_AUTHZ_PATH,
            headers={**member_headers(tenant_id=TENANT_A), "host": _APP_HOST_A},
        )
        call = mock_spicedb_client.check_permission.await_args
        assert call.kwargs["resource"] == f"application:my-app@{TENANT_A.replace('-', '_')}"
        assert call.kwargs["permission"] == "can_access"

    @pytest.mark.asyncio
    async def test_app_hostname_encodes_app_name_in_resource(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        host = f"sales-dashboard.{TENANT_A}.cortex.ai"
        await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": host},
        )
        call = mock_spicedb_client.check_permission.await_args
        assert "sales" in call.kwargs["resource"] or "sales_dashboard" in call.kwargs["resource"]
        assert call.kwargs["permission"] == "can_access"


# ---------------------------------------------------------------------------
# TC-3  Cross-tenant hostname — SpiceDB denies since user has no accessor
# ---------------------------------------------------------------------------


class TestCrossTenantHostname:
    """A user authenticated on tenant A hitting tenant B's hostname is denied
    by SpiceDB (no accessor relation), not by a pre-flight tenant check.
    Envoy's OIDC policy is the authentication gate; hostname is authoritative
    for which tenant's SpiceDB relations to check."""

    @pytest.mark.asyncio
    async def test_tenant_a_token_on_tenant_b_ui_returns_403(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        mock_spicedb_client.check_permission = _AsyncMock(
            return_value=_CheckResp(allowed=False, checked_at=ZEDTOKEN_READ)
        )
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_B},
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_tenant_a_token_on_tenant_b_app_returns_403(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        mock_spicedb_client.check_permission = _AsyncMock(
            return_value=_CheckResp(allowed=False, checked_at=ZEDTOKEN_READ)
        )
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**member_headers(tenant_id=TENANT_A), "host": _APP_HOST_B},
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_cross_tenant_response_is_not_401(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        mock_spicedb_client.check_permission = _AsyncMock(
            return_value=_CheckResp(allowed=False, checked_at=ZEDTOKEN_READ)
        )
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_B},
        )
        assert response.status_code != 401

    @pytest.mark.asyncio
    async def test_cross_tenant_calls_spicedb_with_hostname_tenant(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        """SpiceDB is called for the tenant this deployment serves.

        It used to be whichever tenant the HOSTNAME named, which is how a
        multi-tenant gateway routes. Here the tenant is configuration, so a
        request arriving on another tenant's hostname is checked against — and
        refused for — the tenant actually being served, rather than quietly
        being answered on that other tenant's behalf.
        """
        mock_spicedb_client.check_permission = _AsyncMock(
            return_value=_CheckResp(allowed=False, checked_at=ZEDTOKEN_READ)
        )
        await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_B},
        )
        mock_spicedb_client.check_permission.assert_called_once()
        call_kwargs = mock_spicedb_client.check_permission.call_args.kwargs
        assert call_kwargs["tenant_id"] == TENANT_A

    @pytest.mark.asyncio
    async def test_matching_tenant_is_allowed(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_B), "host": _UI_HOST_B},
        )
        assert response.status_code == 200


# ---------------------------------------------------------------------------
# TC-4  Unrecognised hostname → 403
# ---------------------------------------------------------------------------


class TestUnrecognisedHostname:
    """Hostnames that do not match <app>.<tenant>.cortex.ai are rejected."""

    @pytest.mark.asyncio
    async def test_bare_hostname_returns_403(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": "unknown.example.com"},
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_test_base_url_returns_403(
        self, client: AsyncClient
    ) -> None:
        """The httpx test base_url 'http://test' sends host: test — must be rejected."""
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers=owner_headers(tenant_id=TENANT_A),
            # No override: host header will be 'test'
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_unrecognised_hostname_has_deny_header(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": "unknown.example.com"},
        )
        assert response.headers["x-authz-decision"] == "deny"


# ---------------------------------------------------------------------------
# TC-5  Missing identity headers → 401
# ---------------------------------------------------------------------------


class TestExtAuthzUnauthenticated:
    """Absent or empty identity headers must produce 401, never 403."""

    @pytest.mark.asyncio
    async def test_no_identity_headers_returns_401(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={"host": _UI_HOST_A},
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_missing_tenant_header_returns_401(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={
                "host": _UI_HOST_A,
                "x-cortex-sub": USER_MEMBER_A,
            },
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_missing_sub_header_returns_401(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={
                "host": _UI_HOST_A,
                "x-cortex-tenant": TENANT_A,
            },
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_401_is_not_403(self, client: AsyncClient) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={"host": _UI_HOST_A},
        )
        assert response.status_code != 403


# ---------------------------------------------------------------------------
# TC-6  Health bypass — no identity headers required
# ---------------------------------------------------------------------------


class TestExtAuthzHealthBypass:
    """Probes to /health and /healthz must never be blocked."""

    @pytest.mark.asyncio
    async def test_health_path_without_headers_returns_200(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            json={"path": "/health"},
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_healthz_path_without_headers_returns_200(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            json={"path": "/healthz"},
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_health_via_appended_path_returns_200(
        self, client: AsyncClient
    ) -> None:
        response = await client.get(f"{_EXT_AUTHZ_PATH}/health")
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_health_bypass_skips_spicedb(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        await client.get(f"{_EXT_AUTHZ_PATH}/health")
        mock_spicedb_client.check_permission.assert_not_called()

    @pytest.mark.asyncio
    async def test_health_bypass_has_allow_decision_header(
        self, client: AsyncClient
    ) -> None:
        response = await client.get(f"{_EXT_AUTHZ_PATH}/health")
        assert response.headers["x-authz-decision"] == "allow"


# ---------------------------------------------------------------------------
# TC-7  x-authz-decision header on every response
# ---------------------------------------------------------------------------


class TestExtAuthzDecisionHeader:
    """Every response (200, 401, 403) must carry the x-authz-decision header."""

    @pytest.mark.asyncio
    async def test_allowed_response_has_allow_header(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        assert response.headers.get("x-authz-decision") == "allow"

    @pytest.mark.asyncio
    async def test_unauthenticated_response_has_deny_header(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={"host": _UI_HOST_A},
        )
        assert response.headers.get("x-authz-decision") == "deny"

    @pytest.mark.asyncio
    async def test_cross_tenant_response_has_deny_header(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        mock_spicedb_client.check_permission = _AsyncMock(
            return_value=_CheckResp(allowed=False, checked_at=ZEDTOKEN_READ)
        )
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_B},
        )
        assert response.headers.get("x-authz-decision") == "deny"

    @pytest.mark.asyncio
    async def test_spicedb_deny_response_has_deny_header(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        mock_spicedb_client.check_permission = _AsyncMock(
            return_value=_CheckResp(allowed=False, checked_at=ZEDTOKEN_READ)
        )
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        assert response.headers.get("x-authz-decision") == "deny"


# ---------------------------------------------------------------------------
# TC-8  Allow response forwards identity headers to backend
# ---------------------------------------------------------------------------


class TestExtAuthzIdentityForwarding:
    """On allow, x-cortex-sub and x-cortex-tenant are set on the response so
    upstream apps receive the caller's identity and tenant."""

    @pytest.mark.asyncio
    async def test_allow_response_carries_sub_header(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        assert response.status_code == 200
        # sub is the raw preferred_username from the JWT (without "user:" prefix)
        assert response.headers.get("x-cortex-sub") == USER_OWNER_A.removeprefix("user:")

    @pytest.mark.asyncio
    async def test_allow_response_carries_tenant_header(
        self, client: AsyncClient
    ) -> None:
        """x-cortex-tenant is forwarded so upstream apps know the tenant.

        The tenant is resolved from the hostname; injecting it (rather than
        relying on each app to re-derive it) gives apps a consistent
        identity+tenant contract from the gateway.
        """
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        assert response.status_code == 200
        assert response.headers.get("x-cortex-tenant") == TENANT_A

    @pytest.mark.asyncio
    async def test_allow_response_carries_app_header(
        self, client: AsyncClient
    ) -> None:
        """x-cortex-app is forwarded so upstream apps know which app they were
        addressed as.

        The appname is resolved from the request hostname; injecting it (rather
        than letting each app trust a client-supplied header) makes the gateway
        the authoritative source of ``ctx.app`` for observability and any app-
        scoped policy checks downstream.
        """
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        assert response.status_code == 200
        # _UI_HOST_A = "cortex-ui.<tenant>.cortex.ai" — appname is the first label
        assert response.headers.get("x-cortex-app") == "cortex-ui"

    @pytest.mark.asyncio
    async def test_allow_response_does_not_carry_roles_header(
        self, client: AsyncClient
    ) -> None:
        """Roles header must not be forwarded — roles are no longer part of the model."""
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        assert response.status_code == 200
        assert "x-cortex-roles" not in response.headers


# ---------------------------------------------------------------------------
# TC-9  Token TTL (ADR-004)
# ---------------------------------------------------------------------------


class TestExtAuthzTokenTTL:
    """Tokens with exp - iat > 86400s must be rejected with 403."""

    @pytest.mark.asyncio
    async def test_ttl_exceeding_86400s_returns_403(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers=bearer_headers(tenant_id=TENANT_A, host=_UI_HOST_A, ttl=86401),
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_ttl_exactly_86400s_is_allowed(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        mock_spicedb_client.check_permission = _AsyncMock(
            return_value=_CheckResp(allowed=True, checked_at=ZEDTOKEN_READ)
        )
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers=bearer_headers(tenant_id=TENANT_A, host=_UI_HOST_A, ttl=86400),
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_missing_ttl_headers_does_not_block(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        assert response.status_code == 200


# ---------------------------------------------------------------------------
# TC-10  Bearer token (OIDC flow)
# ---------------------------------------------------------------------------


class TestExtAuthzOIDCBearer:
    """ext_authz accepts identity from Authorization: Bearer (OIDC flow)."""

    @pytest.mark.asyncio
    async def test_bearer_on_ui_returns_200(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers=bearer_headers(host=_UI_HOST_A),
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_bearer_on_app_returns_200(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers=bearer_headers(host=_APP_HOST_A),
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_bearer_cross_tenant_hostname_returns_403(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        mock_spicedb_client.check_permission = _AsyncMock(
            return_value=_CheckResp(allowed=False, checked_at=ZEDTOKEN_READ)
        )
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers=bearer_headers(tenant_id=TENANT_A, host=_UI_HOST_B),
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_bearer_takes_precedence_over_x_cortex_headers(
        self, client: AsyncClient
    ) -> None:
        """Bearer is the sole identity source; x-cortex-* headers are ignored."""
        hdrs = bearer_headers(sub=USER_OWNER_A, tenant_id=TENANT_A, host=_UI_HOST_A)
        hdrs["x-cortex-tenant"] = TENANT_B
        hdrs["x-cortex-sub"] = "user:evil-attacker"
        response = await client.post(_EXT_AUTHZ_PATH, headers=hdrs)
        assert response.status_code == 200
        # sub in response is from the Bearer token, not the forged header
        assert response.headers.get("x-cortex-sub") == USER_OWNER_A.removeprefix("user:")

    @pytest.mark.asyncio
    async def test_invalid_bearer_returns_401(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={"authorization": "Bearer not-a-valid-jwt", "host": _UI_HOST_A},
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_forged_sub_header_cannot_escalate_via_bearer(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        """Bearer wins over x-cortex-sub — the correct (member) subject is denied."""
        def _subject_aware_check(**kwargs):
            sub = kwargs.get("subject", "")
            if USER_MEMBER_A in sub or "member-bob" in sub:
                return _CheckResp(allowed=False, checked_at=ZEDTOKEN_READ)
            return _CheckResp(allowed=True, checked_at=ZEDTOKEN_READ)

        mock_spicedb_client.check_permission = _AsyncMock(side_effect=_subject_aware_check)
        hdrs = bearer_headers(sub=USER_MEMBER_A, tenant_id=TENANT_A, host=_UI_HOST_A)
        hdrs["x-cortex-sub"] = USER_OWNER_A
        response = await client.post(_EXT_AUTHZ_PATH, headers=hdrs)
        assert response.status_code == 403


# ---------------------------------------------------------------------------
# TC-11  Bearer TTL enforcement (ADR-004)
# ---------------------------------------------------------------------------


class TestExtAuthzBearerTokenTTL:
    """Bearer tokens exceeding 24-hour TTL must be rejected."""

    @pytest.mark.asyncio
    async def test_bearer_with_valid_ttl_returns_200(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers=bearer_headers(tenant_id=TENANT_A, host=_UI_HOST_A, ttl=86400),
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_bearer_with_excessive_ttl_returns_403(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers=bearer_headers(tenant_id=TENANT_A, host=_UI_HOST_A, ttl=86401),
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_bearer_ttl_one_second_over_returns_403(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers=bearer_headers(tenant_id=TENANT_A, host=_UI_HOST_A, ttl=86401),
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_bearer_with_missing_exp_iat_is_allowed(
        self, client: AsyncClient
    ) -> None:
        """Tokens without exp/iat skip the TTL check — Envoy already validated the token."""
        claims = {
            "sub": USER_OWNER_A.removeprefix("user:"),
            "preferred_username": USER_OWNER_A.removeprefix("user:"),
            "tid": TENANT_A,
        }
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={"authorization": f"Bearer {_make_jwt(claims)}", "host": _UI_HOST_A},
        )
        assert response.status_code == 200


# ---------------------------------------------------------------------------
# TC-12  SpiceDB enforcement
# ---------------------------------------------------------------------------


class TestExtAuthzSpiceDBEnforcement:
    """Allowing a request requires a successful SpiceDB CheckPermission."""

    @pytest.mark.asyncio
    async def test_allow_calls_spicedb(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        mock_spicedb_client.check_permission.assert_awaited()

    @pytest.mark.asyncio
    async def test_spicedb_deny_returns_403(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        mock_spicedb_client.check_permission = _AsyncMock(
            return_value=_CheckResp(allowed=False, checked_at=ZEDTOKEN_READ)
        )
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        assert response.status_code == 403
        assert response.headers["x-authz-decision"] == "deny"

    @pytest.mark.asyncio
    async def test_spicedb_unavailable_returns_503(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        from data.spicedb_client import SpiceDBUnavailableError
        mock_spicedb_client.check_permission = _AsyncMock(
            side_effect=SpiceDBUnavailableError("simulated outage")
        )
        response = await client.post(
            _EXT_AUTHZ_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        assert response.status_code == 503
        assert response.headers["x-authz-decision"] == "deny"
