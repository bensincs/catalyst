from __future__ import annotations

import base64
import json
import logging
from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any

logger = logging.getLogger(__name__)

# Subject (user identifier) claim preference order, used only when the tenant
# sets no explicit identityProvider.emailClaim. Covers the common OIDC/Entra
# shapes; a standards-compliant IdP that emits only `email` (e.g. Dex, Keycloak)
# falls through to it, and every IdP carries `sub` as the final fallback.
_SUBJECT_CHAIN = ("unique_name", "preferred_username", "upn", "email")


@dataclass
class IdentityContext:
    """Identity read from a validated OIDC token: the subject plus the token's
    lifetime. Deliberately carries no tenant — the Cortex tenant is derived from
    the request host in ext_authz, never from the token."""

    sub: str
    token_exp: int | None = None
    token_iat: int | None = None


def extract_bearer_token(authorization: str | None) -> str | None:
    if not authorization:
        return None
    parts = authorization.strip().split(None, 1)
    if len(parts) != 2 or parts[0].lower() != "bearer":
        return None
    token = parts[1].strip()
    return token if token else None


def _claim_str(claims: Mapping[str, Any], key: str) -> str | None:
    val = claims.get(key)
    return val if isinstance(val, str) and val else None


def decode_access_token(
    raw_jwt: str,
    user_identifier_claim: str | None = None,
) -> IdentityContext | None:
    """Decode an OIDC JWT into identity (subject + token lifetime).

    Entra is just OIDC, so there is one flow: the subject comes from the
    tenant's configured `emailClaim` if set, else the default claim chain, else
    the opaque `sub`. The Cortex tenant is NOT read from the token — it is
    host-authoritative in ext_authz (`<app>.<tenant>.cortex.ai`); Entra's
    `tid`/`azp` is the Entra directory, not the Cortex tenant, so it is ignored.
    Signature/issuer/audience are validated upstream (Envoy Gateway OIDC).

    Returns None when the token is unparseable or carries no usable subject.
    """
    if not raw_jwt:
        return None
    parts = raw_jwt.split(".")
    if len(parts) != 3:
        return None
    try:
        payload_b64 = parts[1]
        padding = 4 - len(payload_b64) % 4
        if padding != 4:
            payload_b64 += "=" * padding
        payload_bytes = base64.urlsafe_b64decode(payload_b64)
        claims = json.loads(payload_bytes)
    except Exception:
        return None

    if not isinstance(claims, dict):
        return None

    # Log claim *keys* only — never values. The JWT payload contains PII
    # (sub, email, oid, tenant identifiers, custom IdP claims) and used to be
    # dumped verbatim at DEBUG, which leaked to pod stdout in every env because
    # the root logger was unconditionally DEBUG. Keys alone are enough to
    # confirm decoding and diagnose missing-claim shapes without exposing the
    # subject.
    if logger.isEnabledFor(logging.DEBUG):
        logger.debug("decoded_token_claim_keys %s", sorted(claims.keys()))

    if user_identifier_claim:
        sub = _claim_str(claims, user_identifier_claim)
    else:
        sub = next((c for k in _SUBJECT_CHAIN if (c := _claim_str(claims, k))), None)
    sub = sub or _claim_str(claims, "sub")
    if not sub:
        return None

    try:
        raw_exp = claims.get("exp")
        raw_iat = claims.get("iat")
        token_exp = int(raw_exp) if raw_exp is not None else None
        token_iat = int(raw_iat) if raw_iat is not None else None
    except (ValueError, TypeError):
        token_exp = None
        token_iat = None

    return IdentityContext(sub=sub, token_exp=token_exp, token_iat=token_iat)
