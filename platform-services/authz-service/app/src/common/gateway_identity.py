"""Proving who a browser request came from.

The admin UI cannot hold a shared token, so its endpoints have to establish
identity some other way. The obvious choice — trusting the X-Cortex-* headers
the gateway stamps — does not survive contact with the cluster: a Service's
ClusterIP is reachable without going through the gateway at all, so any pod can
send those headers itself and be believed. That is a privilege escalation to
administrator for anything running here, and it was demonstrated with a plain
curl from another namespace.

So the token is verified instead. oauth2-proxy forwards the OIDC ID token it
obtained during login, and only a caller that actually completed that login has
one Entra will sign for. A forged header carries no such token, and a stolen
header cannot manufacture one.

Signing keys are fetched from the issuer and cached, because fetching them per
request would put an outbound call on the path of every page load.
"""

from __future__ import annotations

import logging
import os
import time

import jwt
from jwt import PyJWKClient

logger = logging.getLogger(__name__)

_JWKS_CACHE_SECONDS = 3600

_client: PyJWKClient | None = None
_client_made_at: float = 0.0


def issuer() -> str:
    return os.environ.get("AUTHZ_OIDC_ISSUER", "").strip()


def audience() -> str:
    return os.environ.get("AUTHZ_OIDC_AUDIENCE", "").strip()


def configured() -> bool:
    """Whether this deployment can verify tokens at all."""
    return bool(issuer() and audience())


def _jwks_client() -> PyJWKClient:
    global _client, _client_made_at
    now = time.time()
    if _client is None or now - _client_made_at > _JWKS_CACHE_SECONDS:
        # Entra's issuer ends in /v2.0 while its keys hang off the tenant root.
        # Appending the well-known suffix to the issuer gives
        # /v2.0/discovery/v2.0/keys, which 404s.
        root = issuer().rstrip("/").removesuffix("/v2.0")
        _client = PyJWKClient(f"{root}/discovery/v2.0/keys", cache_keys=True)
        _client_made_at = now
    return _client


class InvalidToken(Exception):
    """The request carried no usable proof of who is calling."""


def subject_from_token(authorization: str) -> str:
    """Verify a bearer token and return the caller's address.

    Raises InvalidToken for anything that is not a currently valid token issued
    by this tenant's identity provider for this application.
    """
    if not configured():
        raise InvalidToken("token verification is not configured")

    parts = authorization.split(None, 1)
    if len(parts) != 2 or parts[0].lower() != "bearer" or not parts[1].strip():
        raise InvalidToken("no bearer token")
    token = parts[1].strip()

    try:
        key = _jwks_client().get_signing_key_from_jwt(token).key
        claims = jwt.decode(
            token,
            key,
            algorithms=["RS256"],
            audience=audience(),
            issuer=issuer(),
            options={"require": ["exp", "iat"]},
        )
    except Exception as exc:  # noqa: BLE001 — any failure here means "not proven"
        raise InvalidToken(str(exc)) from exc

    # The address a person is known by, and what grants are written against.
    # `sub` is an opaque pairwise identifier in Entra and matches nothing that
    # was ever granted.
    for claim in ("preferred_username", "upn", "email"):
        value = claims.get(claim)
        if isinstance(value, str) and value.strip():
            return value.strip()
    raise InvalidToken("token carries no address")
