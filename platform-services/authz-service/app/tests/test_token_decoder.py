"""Tests for common.token_decoder — extracts identity from OIDC tokens.

Envoy's OIDC filter authenticates the user and forwards the token via
`Authorization: Bearer <jwt>`. The decoder reads identity from the JWT without
re-verifying the signature (Envoy already did that).

Entra is just OIDC, so there is ONE flow: the subject comes from the tenant's
configured emailClaim, else a default claim chain, else the opaque `sub`. The
Cortex tenant is NOT read from the token — it is host-authoritative in ext_authz.

Claim mapping:
  emailClaim | unique_name>preferred_username>upn>email | sub  -> IdentityContext.sub
  exp / iat                                                     -> token_exp / token_iat
"""

from __future__ import annotations

import base64
import json
import time


def _encode_jwt_payload(claims: dict) -> str:
    """Build a fake JWT (header.payload.signature) with the given claims."""
    header = base64.urlsafe_b64encode(json.dumps({"alg": "RS256", "typ": "JWT"}).encode()).rstrip(b"=").decode()
    payload = base64.urlsafe_b64encode(json.dumps(claims).encode()).rstrip(b"=").decode()
    signature = base64.urlsafe_b64encode(b"fake-signature").rstrip(b"=").decode()
    return f"{header}.{payload}.{signature}"


def _claims(sub: str = "alice", **extra: object) -> dict:
    now = int(time.time())
    base = {"sub": sub, "iat": now, "exp": now + 3600}
    base.update(extra)
    return base


class TestDecodeAccessTokenSubject:
    """decode_access_token resolves the subject; the token carries no tenant."""

    def test_valid_token_returns_identity(self) -> None:
        from common.token_decoder import decode_access_token

        ctx = decode_access_token(_encode_jwt_payload(_claims(preferred_username="alice")))
        assert ctx is not None
        assert ctx.sub == "alice"

    def test_no_tenant_on_identity_context(self) -> None:
        """The token never yields a Cortex tenant — the host does."""
        from common.token_decoder import decode_access_token

        ctx = decode_access_token(_encode_jwt_payload(_claims()))
        assert ctx is not None
        assert not hasattr(ctx, "tenant_id")

    def test_dex_token_without_tid_azp_decodes(self) -> None:
        """A generic OIDC (Dex/Keycloak) token carrying only email + sub — no
        Entra tid/azp — must decode; email is the subject."""
        from common.token_decoder import decode_access_token

        ctx = decode_access_token(_encode_jwt_payload(_claims(sub="opaque", email="alice@example.com")))
        assert ctx is not None
        assert ctx.sub == "alice@example.com"

    def test_configured_claim_wins(self) -> None:
        from common.token_decoder import decode_access_token

        claims = _claims(sub="uuid", upn="alice@corp", email="ignored@corp")
        ctx = decode_access_token(_encode_jwt_payload(claims), user_identifier_claim="upn")
        assert ctx is not None
        assert ctx.sub == "alice@corp"

    def test_default_chain_prefers_unique_name(self) -> None:
        from common.token_decoder import decode_access_token

        claims = _claims(sub="uuid", unique_name="alice", preferred_username="bob", email="c@x")
        ctx = decode_access_token(_encode_jwt_payload(claims))
        assert ctx is not None
        assert ctx.sub == "alice"

    def test_falls_back_through_chain_to_email(self) -> None:
        from common.token_decoder import decode_access_token

        ctx = decode_access_token(_encode_jwt_payload(_claims(sub="uuid", email="alice@x")))
        assert ctx is not None
        assert ctx.sub == "alice@x"

    def test_falls_back_to_sub_when_chain_absent(self) -> None:
        from common.token_decoder import decode_access_token

        ctx = decode_access_token(_encode_jwt_payload(_claims(sub="uuid-123")))
        assert ctx is not None
        assert ctx.sub == "uuid-123"

    def test_non_string_preferred_username_falls_back_to_sub(self) -> None:
        from common.token_decoder import decode_access_token

        claims = _claims(sub="real-sub", preferred_username=["alice"])
        ctx = decode_access_token(_encode_jwt_payload(claims))
        assert ctx is not None
        assert ctx.sub == "real-sub"


class TestDecodeAccessTokenRejections:
    def test_returns_none_for_empty_string(self) -> None:
        from common.token_decoder import decode_access_token

        assert decode_access_token("") is None

    def test_returns_none_for_malformed_jwt(self) -> None:
        from common.token_decoder import decode_access_token

        assert decode_access_token("not-a-jwt") is None

    def test_returns_none_for_jwt_with_invalid_base64(self) -> None:
        from common.token_decoder import decode_access_token

        assert decode_access_token("aaa.!!!invalid!!!.bbb") is None

    def test_returns_none_when_no_sub_or_identifier(self) -> None:
        from common.token_decoder import decode_access_token

        # No sub and no chain claim -> no subject -> None.
        assert decode_access_token(_encode_jwt_payload({"aud": "cortex"})) is None

    def test_returns_none_when_sub_is_non_string_and_no_chain(self) -> None:
        from common.token_decoder import decode_access_token

        for bad in (["alice"], {"id": "alice"}, 123):
            assert decode_access_token(_encode_jwt_payload({"sub": bad})) is None


class TestDecodeAccessTokenExpIat:
    def test_token_exp_and_iat_populated(self) -> None:
        from common.token_decoder import decode_access_token

        now = int(time.time())
        ctx = decode_access_token(_encode_jwt_payload(_claims(iat=now, exp=now + 1800)))
        assert ctx is not None
        assert ctx.token_exp == now + 1800
        assert ctx.token_iat == now

    def test_token_exp_iat_none_when_absent(self) -> None:
        from common.token_decoder import decode_access_token

        ctx = decode_access_token(_encode_jwt_payload({"sub": "alice"}))
        assert ctx is not None
        assert ctx.token_exp is None
        assert ctx.token_iat is None

    def test_malformed_exp_does_not_crash(self) -> None:
        """Non-integer exp/iat must fail closed (None), not raise 500."""
        from common.token_decoder import decode_access_token

        ctx = decode_access_token(_encode_jwt_payload({"sub": "alice", "exp": "nan", "iat": "bad"}))
        assert ctx is not None
        assert ctx.token_exp is None
        assert ctx.token_iat is None

    def test_malformed_exp_only_nullifies_both(self) -> None:
        from common.token_decoder import decode_access_token

        now = int(time.time())
        ctx = decode_access_token(_encode_jwt_payload({"sub": "alice", "exp": "garbage", "iat": now}))
        assert ctx is not None
        assert ctx.token_exp is None
        assert ctx.token_iat is None


class TestExtractBearerToken:
    def test_extracts_token_from_valid_bearer(self) -> None:
        from common.token_decoder import extract_bearer_token

        assert extract_bearer_token("Bearer abc.def.ghi") == "abc.def.ghi"

    def test_is_case_insensitive(self) -> None:
        from common.token_decoder import extract_bearer_token

        assert extract_bearer_token("bearer abc.def.ghi") == "abc.def.ghi"

    def test_returns_none_for_empty_string(self) -> None:
        from common.token_decoder import extract_bearer_token

        assert extract_bearer_token("") is None

    def test_returns_none_for_none(self) -> None:
        from common.token_decoder import extract_bearer_token

        assert extract_bearer_token(None) is None

    def test_returns_none_for_non_bearer_scheme(self) -> None:
        from common.token_decoder import extract_bearer_token

        assert extract_bearer_token("Basic dXNlcjpwYXNz") is None

    def test_returns_none_for_bearer_without_token(self) -> None:
        from common.token_decoder import extract_bearer_token

        assert extract_bearer_token("Bearer ") is None

    def test_strips_whitespace(self) -> None:
        from common.token_decoder import extract_bearer_token

        assert extract_bearer_token("Bearer   abc.def.ghi  ") == "abc.def.ghi"
