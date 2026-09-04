"""Tests for the dedicated /check-bootstrap and /check-oidc endpoints.

Security model
--------------
- /check-bootstrap: only accepts ?_bootstrap=<HS256-jwt>. Bearer tokens and
  cookies are rejected. The endpoint is the security boundary; OIDC tenants
  are routed to /check-oidc by the SecurityPolicy.

- /check-oidc: accepts Authorization: Bearer tokens. Matching or invalid
  bootstrap tokens are ignored (post-SSO transition). A bootstrap token
  whose tenant claim does not match the operator-stamped path tenant is
  rejected with 403 so magic links cannot cross tenants.

- /check (legacy): continues to work via _ext_authz_check_impl → _oidc_check.

All three route to the same underlying logic for their respective mode; this
file verifies the routing, rejection behaviour, and that the two endpoints
cannot be confused with each other.
"""

from __future__ import annotations

import time as _time
from unittest.mock import AsyncMock as _AsyncMock

import jwt
import pytest
from httpx import AsyncClient

from data.models import CheckPermissionResponse as _CheckResp

from tests.conftest import (
    TENANT_A,
    TENANT_B,
    ZEDTOKEN_READ,
    bearer_headers,
    owner_headers,
)

_BOOTSTRAP_PATH = f"/v1/ext-authz/t/{TENANT_A}/check-bootstrap"
_OIDC_PATH = f"/v1/ext-authz/t/{TENANT_A}/check-oidc"
_OIDC_PATH_B = f"/v1/ext-authz/t/{TENANT_B}/check-oidc"
_BOOTSTRAP_PATH_B = f"/v1/ext-authz/t/{TENANT_B}/check-bootstrap"
_LEGACY_PATH = "/v1/ext-authz/check"

_UI_HOST_A = f"cortex-tenant-ui.{TENANT_A}.cortex.ai"
_UI_HOST_B = f"cortex-tenant-ui.{TENANT_B}.cortex.ai"
_APP_HOST_A = f"my-app.{TENANT_A}.cortex.ai"

SUBDOMAIN_ALPHA = "sub-alpha"
_UI_HOST_DIVERGENT = f"cortex-tenant-ui.{SUBDOMAIN_ALPHA}.cortex.ai"

# A test signing key — must match what the app sees via CORTEX_BOOTSTRAP_SIGNING_KEY
_TEST_SIGNING_KEY = "test-bootstrap-secret-key"


def _make_bootstrap_jwt(
    sub: str = "admin",
    tenant: str = TENANT_A,
    scope: str = "bootstrap",
    ttl: int = 3600,
    signing_key: str = _TEST_SIGNING_KEY,
) -> str:
    """Build a valid HS256-signed bootstrap JWT."""
    now = int(_time.time())
    return jwt.encode(
        {
            "sub": sub,
            "tenant": tenant,
            "scope": scope,
            "iat": now,
            "exp": now + ttl,
        },
        signing_key,
        algorithm="HS256",
    )


def _generic_oidc_bearer_headers(
    sub: str = "owner-a@example.com",
    host: str | None = None,
    ttl: int = 3600,
) -> dict[str, str]:
    """Bearer headers for a standards-compliant OIDC (Dex/Keycloak) token:
    carries email + sub but NONE of Entra's tid/azp claims. It decodes on the
    single /check-oidc path — the tenant comes from the host."""
    now = int(_time.time())
    claims = {
        "iss": "https://dex.tenant.cortex.ai",
        "sub": "Cg0wLTM4NS0yODA4OS0wEgRtb2Nr",
        "aud": "cortex",
        "email": sub,
        "email_verified": True,
        "iat": now,
        "exp": now + ttl,
    }
    hdrs: dict[str, str] = {"authorization": f"Bearer {jwt.encode(claims, 'k', algorithm='HS256')}"}
    if host:
        hdrs["host"] = host
    return hdrs


# ---------------------------------------------------------------------------
# /check-bootstrap  — happy path
# ---------------------------------------------------------------------------


class TestCheckBootstrapHappyPath:
    """Valid bootstrap token on cortex-tenant-ui → 200."""

    @pytest.mark.asyncio
    async def test_valid_bootstrap_token_returns_200(
        self, client: AsyncClient, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        monkeypatch.setenv("CORTEX_BOOTSTRAP_SIGNING_KEY", _TEST_SIGNING_KEY)
        import importlib
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        token = _make_bootstrap_jwt()
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": _UI_HOST_A},
            params={"_bootstrap": token},
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_valid_bootstrap_returns_allow_decision_header(
        self, client: AsyncClient, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        token = _make_bootstrap_jwt()
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": _UI_HOST_A},
            params={"_bootstrap": token},
        )
        assert response.headers.get("x-authz-decision") == "allow"

    @pytest.mark.asyncio
    async def test_valid_bootstrap_sets_sub_header(
        self, client: AsyncClient, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        token = _make_bootstrap_jwt(sub="admin-user")
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": _UI_HOST_A},
            params={"_bootstrap": token},
        )
        assert response.status_code == 200
        assert response.headers.get("x-cortex-sub") == "admin-user"
        # the resolved tenant (from the host) is injected for the upstream app
        assert response.headers.get("x-cortex-tenant") == TENANT_A


# ---------------------------------------------------------------------------
# /check-bootstrap — rejection cases
# ---------------------------------------------------------------------------


class TestCheckBootstrapRejections:
    """Bootstrap endpoint must reject anything that is not a valid ?_bootstrap= JWT."""

    @pytest.mark.asyncio
    async def test_no_bootstrap_token_returns_401(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": _UI_HOST_A},
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_bearer_token_without_bootstrap_param_returns_401(
        self, client: AsyncClient
    ) -> None:
        """Bearer token alone at /check-bootstrap must be rejected — in bootstrap
        mode there is no IDP to verify the token against, so only bootstrap
        JWTs are accepted."""
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={**owner_headers(tenant_id=TENANT_A), "host": _UI_HOST_A},
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_expired_bootstrap_token_returns_401(
        self, client: AsyncClient
    ) -> None:
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        token = _make_bootstrap_jwt(ttl=-1)
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": _UI_HOST_A},
            params={"_bootstrap": token},
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_wrong_tenant_in_bootstrap_token_returns_403(
        self, client: AsyncClient
    ) -> None:
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        token = _make_bootstrap_jwt(tenant=TENANT_B)
        response = await client.post(
            _BOOTSTRAP_PATH,
            # Trusted path says TENANT_A; token says TENANT_B
            headers={"host": _UI_HOST_A},
            params={"_bootstrap": token},
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_cross_tenant_token_on_other_tenant_path_returns_403(
        self, client: AsyncClient
    ) -> None:
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        token = _make_bootstrap_jwt(tenant=TENANT_B)
        response = await client.post(
            _BOOTSTRAP_PATH_B,
            headers={"host": _UI_HOST_B},
            params={"_bootstrap": token},
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_trusted_tenant_wins_when_host_subdomain_differs(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY
        mock_spicedb_client.check_permission.return_value = _CheckResp(
            allowed=True, checked_at=ZEDTOKEN_READ
        )

        token = _make_bootstrap_jwt(tenant=TENANT_A)
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": _UI_HOST_DIVERGENT},
            params={"_bootstrap": token},
        )
        assert response.status_code == 200
        assert response.headers.get("x-cortex-tenant") == TENANT_A

    @pytest.mark.asyncio
    async def test_non_numeric_port_in_host_returns_403(
        self, client: AsyncClient
    ) -> None:
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        token = _make_bootstrap_jwt(tenant=TENANT_B)
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": f"cortex-tenant-ui.{TENANT_A}.cortex.ai:notaport"},
            params={"_bootstrap": token},
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_non_string_sub_claim_returns_401(
        self, client: AsyncClient
    ) -> None:
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        now = int(_time.time())
        token = jwt.encode(
            {
                "sub": 12345,
                "tenant": TENANT_A,
                "scope": "bootstrap",
                "iat": now,
                "exp": now + 3600,
            },
            _TEST_SIGNING_KEY,
            algorithm="HS256",
        )
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": _UI_HOST_A},
            params={"_bootstrap": token},
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_hostname_case_and_port_normalization(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY
        mock_spicedb_client.check_permission.return_value = _CheckResp(
            allowed=True, checked_at=ZEDTOKEN_READ
        )

        token = _make_bootstrap_jwt(tenant=TENANT_A)
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": f"Cortex-Tenant-UI.{TENANT_A}.cortex.ai:8443"},
            params={"_bootstrap": token},
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_wrong_scope_in_bootstrap_token_returns_401(
        self, client: AsyncClient
    ) -> None:
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        token = _make_bootstrap_jwt(scope="openid")
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": _UI_HOST_A},
            params={"_bootstrap": token},
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_bootstrap_token_on_non_ui_app_returns_403(
        self, client: AsyncClient
    ) -> None:
        """Bootstrap tokens are only valid for cortex-tenant-ui."""
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        token = _make_bootstrap_jwt()
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": _APP_HOST_A},  # my-app, not cortex-tenant-ui
            params={"_bootstrap": token},
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_invalid_signature_returns_401(
        self, client: AsyncClient
    ) -> None:
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        token = _make_bootstrap_jwt(signing_key="wrong-key")
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": _UI_HOST_A},
            params={"_bootstrap": token},
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_unrecognised_hostname_returns_403(
        self, client: AsyncClient
    ) -> None:
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        token = _make_bootstrap_jwt()
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": "unknown.example.com"},
            params={"_bootstrap": token},
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_bootstrap_spicedb_deny_returns_403(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        mock_spicedb_client.check_permission = _AsyncMock(
            return_value=_CheckResp(allowed=False, checked_at=ZEDTOKEN_READ)
        )
        token = _make_bootstrap_jwt()
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": _UI_HOST_A},
            params={"_bootstrap": token},
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_no_bootstrap_has_deny_decision_header(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _BOOTSTRAP_PATH,
            headers={"host": _UI_HOST_A},
        )
        assert response.headers.get("x-authz-decision") == "deny"


# ---------------------------------------------------------------------------
# /check-bootstrap  — health bypass
# ---------------------------------------------------------------------------


class TestCheckBootstrapHealthBypass:
    """Health paths must bypass auth even at /check-bootstrap."""

    @pytest.mark.asyncio
    async def test_health_path_returns_200(
        self, client: AsyncClient
    ) -> None:
        response = await client.get(f"{_BOOTSTRAP_PATH}/health")
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_healthz_path_returns_200(
        self, client: AsyncClient
    ) -> None:
        response = await client.get(f"{_BOOTSTRAP_PATH}/healthz")
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_health_skips_spicedb(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        await client.get(f"{_BOOTSTRAP_PATH}/health")
        mock_spicedb_client.check_permission.assert_not_called()


# ---------------------------------------------------------------------------
# /check-oidc  — happy path
# ---------------------------------------------------------------------------


class TestCheckOIDCHappyPath:
    """OIDC endpoint accepts Bearer tokens and routes to SpiceDB."""

    @pytest.mark.asyncio
    async def test_bearer_token_returns_200(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _OIDC_PATH,
            headers=bearer_headers(host=f"cortex-ui.{TENANT_A}.cortex.ai"),
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_bearer_returns_allow_decision_header(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _OIDC_PATH,
            headers=bearer_headers(host=f"cortex-ui.{TENANT_A}.cortex.ai"),
        )
        assert response.headers.get("x-authz-decision") == "allow"

    @pytest.mark.asyncio
    async def test_bearer_sets_sub_header(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _OIDC_PATH,
            headers=bearer_headers(host=f"cortex-ui.{TENANT_A}.cortex.ai"),
        )
        assert response.status_code == 200
        assert "x-cortex-sub" in response.headers
        # the resolved tenant (from the host) is injected for the upstream app
        assert response.headers.get("x-cortex-tenant") == TENANT_A

    @pytest.mark.asyncio
    async def test_spicedb_deny_returns_403(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        mock_spicedb_client.check_permission = _AsyncMock(
            return_value=_CheckResp(allowed=False, checked_at=ZEDTOKEN_READ)
        )
        response = await client.post(
            _OIDC_PATH,
            headers=bearer_headers(host=f"cortex-ui.{TENANT_A}.cortex.ai"),
        )
        assert response.status_code == 403


# ---------------------------------------------------------------------------
# /check-oidc — one OIDC flow (Entra is just OIDC; tenant is host-authoritative)
# ---------------------------------------------------------------------------


class TestCheckOIDCGenericToken:
    """A standards-compliant OIDC token that carries no Entra tid/azz (e.g. Dex,
    Keycloak) is accepted on the single /check-oidc path — the Cortex tenant is
    taken from the host, never from the token."""

    @pytest.mark.asyncio
    async def test_generic_token_accepted_on_oidc_path(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _OIDC_PATH,
            headers=_generic_oidc_bearer_headers(host=f"my-app.{TENANT_A}.cortex.ai"),
        )
        assert response.status_code == 200
        assert response.headers.get("x-authz-decision") == "allow"

    @pytest.mark.asyncio
    async def test_tenant_resolved_from_host_not_token(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _OIDC_PATH,
            headers=_generic_oidc_bearer_headers(host=f"my-app.{TENANT_A}.cortex.ai"),
        )
        assert response.status_code == 200
        # Tenant comes from the host; the token has no tid/azp.
        assert response.headers.get("x-cortex-tenant") == TENANT_A
        assert response.headers.get("x-cortex-sub") == "owner-a@example.com"

    @pytest.mark.asyncio
    async def test_oidc_path_still_rejects_missing_bearer(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _OIDC_PATH,
            headers={"host": f"my-app.{TENANT_A}.cortex.ai"},
        )
        assert response.status_code == 401


# ---------------------------------------------------------------------------
# /check-oidc  — rejection cases
# ---------------------------------------------------------------------------


class TestCheckOIDCRejections:
    """OIDC endpoint must reject requests without a valid Bearer token."""

    @pytest.mark.asyncio
    async def test_no_bearer_returns_401(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _OIDC_PATH,
            headers={"host": f"cortex-ui.{TENANT_A}.cortex.ai"},
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_invalid_bearer_returns_401(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _OIDC_PATH,
            headers={
                "authorization": "Bearer not-a-valid-jwt",
                "host": f"cortex-ui.{TENANT_A}.cortex.ai",
            },
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_bootstrap_param_without_bearer_returns_401(
        self, client: AsyncClient
    ) -> None:
        """A same-tenant ?_bootstrap= token at /check-oidc is ignored — the
        endpoint only accepts Bearer tokens. No Bearer token means 401."""
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        token = _make_bootstrap_jwt()
        response = await client.post(
            _OIDC_PATH,
            headers={"host": _UI_HOST_A},
            params={"_bootstrap": token},
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_bootstrap_param_with_bearer_ignores_bootstrap(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        """When both a same-tenant bootstrap token and a Bearer token are
        present at /check-oidc, the bootstrap token is ignored and the Bearer
        token is used. This covers the transition period where the browser
        still holds a bootstrap cookie after SSO has been configured."""
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        mock_spicedb_client.check_permission.return_value = _CheckResp(
            allowed=True, checked_at=ZEDTOKEN_READ
        )
        bootstrap_token = _make_bootstrap_jwt()
        response = await client.post(
            _OIDC_PATH,
            headers={
                **bearer_headers(),
                "host": _UI_HOST_A,
                "cookie": f"_bootstrap_token={bootstrap_token}",
            },
        )
        assert response.status_code == 200
        assert response.headers["x-authz-decision"] == "allow"

    @pytest.mark.asyncio
    async def test_cross_tenant_bootstrap_without_bearer_returns_403(
        self, client: AsyncClient
    ) -> None:
        """A bootstrap token for a tenant this deployment does not serve is
        rejected, even before any Bearer token is presented."""
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        token = _make_bootstrap_jwt(tenant=TENANT_B)
        response = await client.post(
            _OIDC_PATH_B,
            headers={"host": _UI_HOST_B},
            params={"_bootstrap": token},
        )
        assert response.status_code == 403
        assert "not valid for this tenant" in response.json()["detail"]

    @pytest.mark.asyncio
    async def test_cross_tenant_bootstrap_with_bearer_returns_403(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        """A foreign bootstrap credential must not ride along on a valid
        OIDC session for a different tenant."""
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        mock_spicedb_client.check_permission.return_value = _CheckResp(
            allowed=True, checked_at=ZEDTOKEN_READ
        )
        token = _make_bootstrap_jwt(tenant=TENANT_B)
        response = await client.post(
            _OIDC_PATH_B,
            headers={
                **bearer_headers(tenant_id=TENANT_B),
                "host": _UI_HOST_B,
            },
            params={"_bootstrap": token},
        )
        assert response.status_code == 403
        assert "not valid for this tenant" in response.json()["detail"]
        mock_spicedb_client.check_permission.assert_not_called()

    @pytest.mark.asyncio
    async def test_garbage_bootstrap_param_with_bearer_is_ignored(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        """Garbage ?_bootstrap= on /check-oidc must not 403 — only signed
        tenant mismatches deny (DoS-prevention asymmetry vs the UI proxy)."""
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        mock_spicedb_client.check_permission.return_value = _CheckResp(
            allowed=True, checked_at=ZEDTOKEN_READ
        )
        response = await client.post(
            _OIDC_PATH_B,
            headers={
                **bearer_headers(tenant_id=TENANT_B),
                "host": _UI_HOST_B,
            },
            params={"_bootstrap": "not-a-jwt"},
        )
        assert response.status_code == 200
        assert response.headers["x-authz-decision"] == "allow"
        mock_spicedb_client.check_permission.assert_called_once()

    @pytest.mark.asyncio
    async def test_garbage_bootstrap_param_without_bearer_returns_401(
        self, client: AsyncClient
    ) -> None:
        """Garbage ?_bootstrap= without Bearer is ignored → 401, not 403."""
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        response = await client.post(
            _OIDC_PATH_B,
            headers={"host": _UI_HOST_B},
            params={"_bootstrap": "not-a-jwt"},
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_forged_cross_tenant_bootstrap_with_bearer_is_ignored(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        """Wrong-signature bootstrap with a foreign tenant claim must not
        become a mismatch 403 — only signed mismatches deny."""
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        mock_spicedb_client.check_permission.return_value = _CheckResp(
            allowed=True, checked_at=ZEDTOKEN_READ
        )
        forged = _make_bootstrap_jwt(tenant=TENANT_A, signing_key="wrong-key")
        response = await client.post(
            _OIDC_PATH_B,
            headers={
                **bearer_headers(tenant_id=TENANT_B),
                "host": _UI_HOST_B,
            },
            params={"_bootstrap": forged},
        )
        assert response.status_code == 200
        assert response.headers["x-authz-decision"] == "allow"
        mock_spicedb_client.check_permission.assert_called_once()

    @pytest.mark.asyncio
    async def test_cross_tenant_bootstrap_cookie_with_bearer_returns_403(
        self, client: AsyncClient, mock_spicedb_client: _AsyncMock
    ) -> None:
        """Same isolation when the foreign bootstrap token arrives via the
        UI proxy cookie rather than the query param."""
        import routes.ext_authz as _mod
        _mod._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

        mock_spicedb_client.check_permission.return_value = _CheckResp(
            allowed=True, checked_at=ZEDTOKEN_READ
        )
        token = _make_bootstrap_jwt(tenant=TENANT_B)
        response = await client.post(
            _OIDC_PATH_B,
            headers={
                **bearer_headers(tenant_id=TENANT_B),
                "host": _UI_HOST_B,
                "cookie": f"_bootstrap_token={token}",
            },
        )
        assert response.status_code == 403
        mock_spicedb_client.check_permission.assert_not_called()

    @pytest.mark.asyncio
    async def test_unrecognised_hostname_returns_403(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _OIDC_PATH,
            headers={**bearer_headers(), "host": "unknown.example.com"},
        )
        assert response.status_code == 403


# ---------------------------------------------------------------------------
# /check-oidc — health bypass
# ---------------------------------------------------------------------------


class TestCheckOIDCHealthBypass:
    """Health paths must bypass auth even at /check-oidc."""

    @pytest.mark.asyncio
    async def test_health_path_returns_200(
        self, client: AsyncClient
    ) -> None:
        response = await client.get(f"{_OIDC_PATH}/health")
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_healthz_path_returns_200(
        self, client: AsyncClient
    ) -> None:
        response = await client.get(f"{_OIDC_PATH}/healthz")
        assert response.status_code == 200


# ---------------------------------------------------------------------------
# Legacy /check endpoint parity
# ---------------------------------------------------------------------------


class TestLegacyCheckEndpoint:
    """Legacy /check must continue to work via the OIDC path (backwards compat)."""

    @pytest.mark.asyncio
    async def test_legacy_check_accepts_bearer_returns_200(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _LEGACY_PATH,
            headers=bearer_headers(host=f"cortex-ui.{TENANT_A}.cortex.ai"),
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_legacy_check_no_bearer_returns_401(
        self, client: AsyncClient
    ) -> None:
        response = await client.post(
            _LEGACY_PATH,
            headers={"host": f"cortex-ui.{TENANT_A}.cortex.ai"},
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_legacy_check_health_bypass(
        self, client: AsyncClient
    ) -> None:
        response = await client.get(f"{_LEGACY_PATH}/health")
        assert response.status_code == 200



def _patch_signing_key(mod: object, key: str) -> None:
    import routes.ext_authz as _m
    _m._BOOTSTRAP_SIGNING_KEY = key


class TestBootstrapCookieSession:
    """On first magic-link request the response sets a session cookie.
    Subsequent requests (e.g. static assets) use the cookie instead of the
    query param and must be allowed."""

    def _patch(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setenv("CORTEX_BOOTSTRAP_SIGNING_KEY", _TEST_SIGNING_KEY)
        import routes.ext_authz as _m
        _m._BOOTSTRAP_SIGNING_KEY = _TEST_SIGNING_KEY

    @pytest.mark.asyncio
    async def test_first_request_with_query_param_returns_200(
        self, client: AsyncClient, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        """Initial magic-link request with ?_bootstrap= param is allowed."""
        self._patch(monkeypatch)
        token = _make_bootstrap_jwt()
        response = await client.get(
            _BOOTSTRAP_PATH,
            headers={"host": _UI_HOST_A},
            params={"_bootstrap": token},
        )
        assert response.status_code == 200
        # The authz-service does NOT set the cookie — that is the UI proxy's job.
        assert "set-cookie" not in response.headers

    @pytest.mark.asyncio
    async def test_cookie_request_without_query_param_returns_200(
        self, client: AsyncClient, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        """Subsequent request (no ?_bootstrap=) authenticated via cookie."""
        self._patch(monkeypatch)
        token = _make_bootstrap_jwt()
        response = await client.get(
            _BOOTSTRAP_PATH + "/_next/static/chunk.js",
            headers={
                "host": _UI_HOST_A,
                "cookie": f"_bootstrap_token={token}",
            },
        )
        assert response.status_code == 200

    @pytest.mark.asyncio
    async def test_cookie_request_does_not_re_set_cookie(
        self, client: AsyncClient, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        """When authenticated via cookie (not query param) no Set-Cookie is returned."""
        self._patch(monkeypatch)
        token = _make_bootstrap_jwt()
        response = await client.get(
            _BOOTSTRAP_PATH + "/_next/static/chunk.js",
            headers={
                "host": _UI_HOST_A,
                "cookie": f"_bootstrap_token={token}",
            },
        )
        assert response.status_code == 200
        assert "set-cookie" not in response.headers

    @pytest.mark.asyncio
    async def test_expired_token_in_cookie_returns_401(
        self, client: AsyncClient, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        self._patch(monkeypatch)
        expired_token = _make_bootstrap_jwt(ttl=-1)
        response = await client.get(
            _BOOTSTRAP_PATH + "/_next/static/chunk.js",
            headers={
                "host": _UI_HOST_A,
                "cookie": f"_bootstrap_token={expired_token}",
            },
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_tampered_cookie_returns_401(
        self, client: AsyncClient, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        self._patch(monkeypatch)
        response = await client.get(
            _BOOTSTRAP_PATH + "/_next/static/chunk.js",
            headers={
                "host": _UI_HOST_A,
                "cookie": "_bootstrap_token=not.a.valid.jwt",
            },
        )
        assert response.status_code == 401

    @pytest.mark.asyncio
    async def test_wrong_tenant_in_cookie_on_wrong_host_returns_403(
        self, client: AsyncClient, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        self._patch(monkeypatch)
        token = _make_bootstrap_jwt(tenant=TENANT_B)
        response = await client.get(
            _BOOTSTRAP_PATH + "/_next/static/chunk.js",
            headers={
                "host": _UI_HOST_A,
                "cookie": f"_bootstrap_token={token}",
            },
        )
        assert response.status_code == 403

    @pytest.mark.asyncio
    async def test_no_auth_on_static_asset_returns_401(
        self, client: AsyncClient, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        """No query param and no cookie → still 401, not a bypass."""
        self._patch(monkeypatch)
        response = await client.get(
            _BOOTSTRAP_PATH + "/_next/static/chunk.js",
            headers={"host": _UI_HOST_A},
        )
        assert response.status_code == 401
