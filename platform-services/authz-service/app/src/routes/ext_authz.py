from __future__ import annotations

import logging
import os
import re
from typing import NamedTuple

import jwt
from fastapi import APIRouter, Depends, Request
from fastapi.responses import JSONResponse

from common.audit import AuditEvent, elapsed_ms, request_context_from, subject_id
from common.tenant import TENANT_ID
from common.dependencies import get_authz_client
from common.token_decoder import decode_access_token, extract_bearer_token
from data.spicedb_client import SpiceDBClient, SpiceDBUnavailableError, sanitize_tenant_id

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/v1/ext-authz")

_HEALTH_PATHS = {"/health", "/healthz"}
_MAX_TOKEN_TTL_SECONDS = 86400
_ALL_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"]

# Matches <appname>.<subdomain>.<ROUTING_DOMAIN> (with optional numeric port). The
# apex domain is sourced from ROUTING_DOMAIN so it can be switched per environment
# without a code change; it MUST match the domain the tenant-operator bakes into
# the Gateway/OIDC hostnames, or every request is denied with a 403.
def _routing_domain_from_env() -> str:
    raw = os.environ.get("ROUTING_DOMAIN", "").strip()
    if not raw:
        raise RuntimeError("ROUTING_DOMAIN is not set")
    return raw


_ROUTING_DOMAIN = _routing_domain_from_env()
_HOSTNAME_PATTERN = re.compile(
    rf"^([^.]+)\.([^.]+)\.{re.escape(_ROUTING_DOMAIN)}(?::\d+)?$"
)

# Bootstrap token constants.
_BOOTSTRAP_PARAM = "_bootstrap"
_BOOTSTRAP_SCOPE = "bootstrap"
_BOOTSTRAP_APP = "cortex-tenant-ui"
_BOOTSTRAP_COOKIE = "_bootstrap_token"
_BOOTSTRAP_TENANT_MISMATCH_DETAIL = "Bootstrap token is not valid for this tenant"
_BOOTSTRAP_SIGNING_KEY: str | None = os.environ.get("CORTEX_BOOTSTRAP_SIGNING_KEY")
# One-time warn if a request hits validation while the key is still unset
# (covers runtime clear / late misconfig; import-time warn is separate).
_BOOTSTRAP_SIGNING_KEY_MISSING_WARNED = False


if not _BOOTSTRAP_SIGNING_KEY:
    logger.warning(
        "CORTEX_BOOTSTRAP_SIGNING_KEY is not set — bootstrap magic-link validation will be unavailable"
    )


def _is_health_path(path: str) -> bool:
    return path in _HEALTH_PATHS


def _parse_hostname(host: str) -> tuple[str, str] | None:
    """Parse ``<app>.<subdomain>.<ROUTING_DOMAIN>`` from the Host header.

    Returns (appname, subdomain). The subdomain is the routing label only;
    the authoritative tenant ID comes from the operator-stamped ext_authz path.
    """
    host = host.strip().lower()
    m = _HOSTNAME_PATTERN.match(host)
    if not m:
        return None
    return m.group(1), m.group(2)


def _resolve_resource_permission(appname: str, tenant_id: str) -> tuple[str, str]:
    safe_tenant = sanitize_tenant_id(tenant_id)
    return f"application:{appname}@{safe_tenant}", "can_access"


def _allow_response(sub: str, tenant: str = "", app: str = "") -> JSONResponse:
    # Inject the caller's identity (x-cortex-sub) and, when known, the resolved
    # tenant (x-cortex-tenant) and application (x-cortex-app) as upstream
    # request headers so downstream apps receive a consistent identity+tenant+app
    # contract from the gateway. Envoy is configured to forward these on an
    # allow decision (SecurityPolicy.HeadersToBackend), and Envoy's ext-authz
    # semantics replace any same-named header the client sent — so these three
    # are the trust boundary for tenant/app attribution downstream.
    headers = {"x-authz-decision": "allow", "x-cortex-sub": sub}
    if tenant:
        headers["x-cortex-tenant"] = tenant
    if app:
        headers["x-cortex-app"] = app
    return JSONResponse(
        status_code=200,
        content={"allowed": True},
        headers=headers,
    )


def _deny_response(status_code: int, detail: str) -> JSONResponse:
    return JSONResponse(
        status_code=status_code,
        content={"detail": detail},
        headers={"x-authz-decision": "deny"},
    )


def _normalize_subject(sub: str) -> str:
    return sub if ":" in sub else f"user:{sub}"


def _extract_bootstrap_token(request: Request) -> str | None:
    """Extract bootstrap token from ?_bootstrap= query param or _bootstrap_token cookie.

    The UI proxy (proxy.ts) is responsible for setting the cookie when it sees
    the token in the URL. Subsequent requests (static assets etc.) will carry
    the cookie but not the query param.
    """
    token = request.query_params.get(_BOOTSTRAP_PARAM) or None
    if token:
        return token
    # Fall back to cookie set by the UI proxy.
    cookie_header = request.headers.get("cookie", "")
    for part in cookie_header.split(";"):
        name, _, value = part.strip().partition("=")
        if name.strip() == _BOOTSTRAP_COOKIE and value.strip():
            return value.strip()
    return None


class _BootstrapValidation(NamedTuple):
    sub: str | None
    tenant_mismatch: bool = False


def _validate_bootstrap_token(raw_token: str, tenant: str) -> _BootstrapValidation:
    """Validate a bootstrap JWT and return the sub claim if valid.

    Checks:
    - Signature using CORTEX_BOOTSTRAP_SIGNING_KEY (HS256)
    - exp claim (PyJWT raises ExpiredSignatureError automatically)
    - scope == "bootstrap"
    - tenant claim matches the trusted tenant ID from the ext_authz path
    - app is cortex-tenant-ui (bootstrap tokens are only valid for that app)
    """
    if not _BOOTSTRAP_SIGNING_KEY:
        global _BOOTSTRAP_SIGNING_KEY_MISSING_WARNED
        if not _BOOTSTRAP_SIGNING_KEY_MISSING_WARNED:
            logger.warning(
                "CORTEX_BOOTSTRAP_SIGNING_KEY unset — skipping bootstrap "
                "tenant-mismatch enforcement (OIDC and bootstrap paths)"
            )
            _BOOTSTRAP_SIGNING_KEY_MISSING_WARNED = True
        return _BootstrapValidation(None)
    try:
        claims = jwt.decode(
            raw_token,
            _BOOTSTRAP_SIGNING_KEY,
            algorithms=["HS256"],
            options={"require": ["sub", "exp", "iat", "scope", "tenant"]},
        )
    except jwt.ExpiredSignatureError:
        logger.info("bootstrap token expired")
        return _BootstrapValidation(None)
    except jwt.InvalidTokenError as exc:
        logger.info("bootstrap token invalid: %s", exc)
        return _BootstrapValidation(None)

    if claims.get("scope") != _BOOTSTRAP_SCOPE:
        logger.info("bootstrap token has wrong scope: %s", claims.get("scope"))
        return _BootstrapValidation(None)
    token_tenant = claims.get("tenant")
    if not isinstance(token_tenant, str) or not token_tenant:
        logger.info("bootstrap token missing tenant claim")
        return _BootstrapValidation(None)
    if token_tenant != tenant:
        logger.info("bootstrap token tenant mismatch")
        return _BootstrapValidation(None, tenant_mismatch=True)

    sub = claims.get("sub")
    if not isinstance(sub, str) or not sub:
        logger.info("bootstrap token missing or invalid sub claim")
        return _BootstrapValidation(None)

    return _BootstrapValidation(sub)


async def _ext_authz_check_impl(
    request: Request,
    full_path: str,
    authz: SpiceDBClient,
    *,
    oidc_mode: bool = False,
    trusted_tenant: str | None = None,
) -> JSONResponse:
    rc = request_context_from(request)

    # Resolve the original path for health bypass.
    path = ("/" + full_path) if full_path else (request.headers.get("x-envoy-original-path") or "")
    if not path:
        try:
            raw = await request.body()
            if raw:
                data = await request.json()
                if isinstance(data, dict):
                    path = data.get("path") or ""
        except Exception:
            # Body may be empty, non-JSON, or malformed — fall through to
            # `request.url.path` below. The path is only used for health-
            # check bypass; if we can't resolve it here, the downstream
            # authorisation check still runs against whatever path we do
            # have, so swallowing the exception is safe.
            pass
    if not path:
        path = request.url.path

    if _is_health_path(path):
        AuditEvent(
            tenant_id="system", subject="probe", action="ext_authz.check",
            resource=path, decision="allow", reason="health_bypass",
            request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
            metadata={"method": request.method},
        ).emit()
        return _allow_response("probe")

    host_header = request.headers.get("host", "").strip()
    parsed = _parse_hostname(host_header) if host_header else None
    if parsed is None:
        AuditEvent(
            tenant_id=trusted_tenant or "unknown", subject="anonymous", action="ext_authz.check",
            resource=path, decision="deny",
            reason=f"unrecognised hostname: {host_header!r}",
            request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
            metadata={"host": host_header},
        ).emit()
        return _deny_response(
            403,
            f"Hostname '{host_header}' does not match expected pattern <app>.<tenant>.{_ROUTING_DOMAIN}",
        )

    appname, _subdomain = parsed

    # Single-tenant: the tenant is configuration, never anything a caller can
    # influence. A trusted path segment that disagrees is a misconfiguration
    # worth surfacing rather than honouring.
    if trusted_tenant is not None and trusted_tenant != TENANT_ID:
        logger.warning(
            "ext_authz path names tenant %r but this deployment serves %r; using the configured tenant",
            trusted_tenant,
            TENANT_ID,
        )
    tenant = TENANT_ID

    host = host_header

    # ------------------------------------------------------------------
    # Bootstrap token path: ?_bootstrap=<jwt> present.
    # The token's own claims (scope, tenant, expiry, HMAC signature) are
    # the security boundary — we no longer rely on x-cortex-bootstrap from
    # Envoy context extensions, which are not forwarded to HTTP ext-authz.
    # ------------------------------------------------------------------
    bootstrap_raw = _extract_bootstrap_token(request)
    # Bring-up diagnostic — one line per gateway hop is too chatty for INFO once
    # the bootstrap flow is stable. Keep at DEBUG so it's available locally.
    logger.debug(
        "bootstrap_raw_present=%s oidc_mode=%s",
        bootstrap_raw is not None,
        oidc_mode,
    )

    # ------------------------------------------------------------------
    # OIDC mode may still receive a bootstrap token (query/cookie leftover).
    # Bootstrap never grants access here, but a tenant mismatch must be
    # rejected so a magic link cannot be replayed on another tenant's
    # gateway before Bearer/OIDC runs. Matching or invalid tokens are
    # ignored so post-SSO cleanup can continue.
    # ------------------------------------------------------------------
    if bootstrap_raw is not None and oidc_mode:
        bootstrap_result = _validate_bootstrap_token(bootstrap_raw, tenant)
        if bootstrap_result.tenant_mismatch:
            AuditEvent(
                tenant_id=tenant, subject="bootstrap", action="ext_authz.check",
                resource=host, decision="deny",
                reason="bootstrap token tenant mismatch",
                request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
                metadata={"oidc_mode": True},
            ).emit()
            return _deny_response(
                403,
                _BOOTSTRAP_TENANT_MISMATCH_DETAIL,
            )

    if bootstrap_raw is not None and not oidc_mode:
        # Bootstrap tokens are only valid for cortex-tenant-ui.
        if appname != _BOOTSTRAP_APP:
            AuditEvent(
                tenant_id=tenant, subject="bootstrap", action="ext_authz.check",
                resource=host, decision="deny", reason=f"bootstrap token not valid for app: {appname}",
                request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
                metadata={"appname": appname},
            ).emit()
            return _deny_response(403, "Bootstrap tokens are only valid for the tenant admin UI")

        bootstrap_result = _validate_bootstrap_token(bootstrap_raw, tenant)
        if bootstrap_result.sub is None:
            if bootstrap_result.tenant_mismatch:
                AuditEvent(
                    tenant_id=tenant, subject="bootstrap", action="ext_authz.check",
                    resource=host, decision="deny",
                    reason="bootstrap token tenant mismatch",
                    request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
                    metadata={},
                ).emit()
                return _deny_response(
                    403,
                    _BOOTSTRAP_TENANT_MISMATCH_DETAIL,
                )
            AuditEvent(
                tenant_id=tenant, subject="bootstrap", action="ext_authz.check",
                resource=host, decision="deny", reason="bootstrap token invalid or expired",
                request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
                metadata={},
            ).emit()
            return _deny_response(401, "Bootstrap token is invalid or expired")

        bootstrap_sub = bootstrap_result.sub

        resource, permission = _resolve_resource_permission(appname, tenant)
        bootstrap_subject = f"bootstrap:{tenant}"
        try:
            result = await authz.check_permission(
                tenant_id=tenant,
                resource=resource,
                permission=permission,
                subject=bootstrap_subject,
            )
        except SpiceDBUnavailableError:
            # `subject` intentionally omitted — bootstrap_sub is the raw JWT
            # sub. Identity is carried by cortex-otel's enduser.id_hash on
            # the log record. See ADR-26-07-21.
            AuditEvent(
                tenant_id=tenant, action="ext_authz.check",
                resource=host, decision="deny", reason="spicedb_unavailable",
                request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
                metadata={"authz_resource": resource, "permission": permission},
            ).emit()
            return _deny_response(503, "authorization service unavailable")

        if not result.allowed:
            AuditEvent(
                tenant_id=tenant, action="ext_authz.check",
                resource=host, decision="deny", reason="bootstrap_denied",
                request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
                metadata={"authz_resource": resource, "permission": permission},
            ).emit()
            return _deny_response(403, "Bootstrap token does not grant access to this resource")

        AuditEvent(
            tenant_id=tenant, action="ext_authz.check",
            resource=host, decision="allow", reason="bootstrap_bypass",
            request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
            metadata={"authz_resource": resource, "permission": permission},
        ).emit()
        return _allow_response(bootstrap_sub, tenant, appname)

    # ------------------------------------------------------------------
    # In bootstrap mode with no bootstrap token present — deny immediately.
    # We have no IDP configured so we cannot verify any Bearer token.
    # ------------------------------------------------------------------
    if not oidc_mode and bootstrap_raw is None:
        AuditEvent(
            tenant_id=tenant, subject="anonymous", action="ext_authz.check",
            resource=path, decision="deny", reason="unauthenticated: no bootstrap token",
            request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
            metadata={},
        ).emit()
        return _deny_response(401, "Unauthorized: no bootstrap token")

    # ------------------------------------------------------------------
    # Standard path: Bearer token from Envoy's OIDC layer.
    # Identity comes exclusively from the Bearer token — Envoy's OIDC policy
    # has already validated the token before this request arrives.
    # The tenant's userIdentifierClaim is forwarded by Envoy as a context
    # extension. For HTTP ext_authz, contextExtensions are sent as headers
    # with their name as the key (lowercased by HTTP/2).
    # ------------------------------------------------------------------
    user_identifier_claim = request.headers.get("useridentifierclaim") or request.headers.get("userIdentifierClaim") or None
    raw_token = extract_bearer_token(request.headers.get("authorization"))
    ctx = decode_access_token(raw_token, user_identifier_claim) if raw_token else None

    if ctx is None:
        AuditEvent(
            tenant_id=tenant, subject="anonymous", action="ext_authz.check",
            resource=path, decision="deny", reason="unauthenticated: no bearer token",
            request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
            metadata={},
        ).emit()
        return _deny_response(401, "Unauthorized: no valid Bearer token")

    # TTL check — Envoy validates the signature but we enforce an additional
    # maximum lifetime to bound the revocation exposure window.
    if ctx.token_exp is not None and ctx.token_iat is not None:
        ttl = ctx.token_exp - ctx.token_iat
        if ttl > _MAX_TOKEN_TTL_SECONDS:
            msg = f"Token TTL ({ttl}s) exceeds maximum allowed ({_MAX_TOKEN_TTL_SECONDS}s)"
            # `subject` intentionally omitted — ctx.sub is the raw JWT sub.
            # Identity is carried by cortex-otel's enduser.id_hash on the
            # log record. See ADR-26-07-21.
            AuditEvent(
                tenant_id=tenant, action="ext_authz.check",
                resource=path, decision="deny", reason=msg,
                request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
                metadata={},
            ).emit()
            return _deny_response(403, msg)

    resource, permission = _resolve_resource_permission(appname, tenant)
    subject = _normalize_subject(ctx.sub)

    try:
        result = await authz.check_permission(
            tenant_id=tenant,
            resource=resource,
            permission=permission,
            subject=subject,
        )
    except SpiceDBUnavailableError:
        AuditEvent(
            tenant_id=tenant, action="ext_authz.check",
            resource=host, decision="deny", reason="spicedb_unavailable",
            request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
            metadata={"authz_resource": resource, "permission": permission},
        ).emit()
        return _deny_response(503, "authorization service unavailable")

    if not result.allowed:
        AuditEvent(
            tenant_id=tenant, action="ext_authz.check",
            resource=host, decision="deny", reason="spicedb_denied",
            request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
            metadata={"authz_resource": resource, "permission": permission},
        ).emit()
        sub_id = subject_id(ctx.sub)
        msg = f"Permission denied: missing '{permission}' on '{resource}'"
        if sub_id:
            # Salted-sha256 pseudonym matching the OTel agent's identity hash
            # (ADR-26-07-21) — lets an operator with salt access correlate
            # the response back to a log record without leaking the raw sub.
            msg += f" (subject {sub_id})"
        return _deny_response(403, msg)

    AuditEvent(
        tenant_id=tenant, action="ext_authz.check",
        resource=host, decision="allow",
        reason=f"{appname}@{tenant}",
        request_id=rc.req_id, source_ip=rc.src_ip, latency_ms=elapsed_ms(rc.start),
        metadata={"authz_resource": resource, "permission": permission},
    ).emit()
    return _allow_response(ctx.sub, tenant, appname)


# ---------------------------------------------------------------------------
# Tenant-scoped routes — the operator stamps metadata.name into the path.
# ---------------------------------------------------------------------------

@router.api_route("/t/{tenant_id}/check-bootstrap", methods=_ALL_METHODS)
@router.api_route("/t/{tenant_id}/check-bootstrap/", methods=_ALL_METHODS)
async def ext_authz_check_bootstrap_tenant(
    request: Request,
    tenant_id: str,
    authz: SpiceDBClient = Depends(get_authz_client),
) -> JSONResponse:
    return await _ext_authz_check_impl(request, "", authz, trusted_tenant=tenant_id)


@router.api_route("/t/{tenant_id}/check-bootstrap/{full_path:path}", methods=_ALL_METHODS)
async def ext_authz_check_bootstrap_tenant_with_path(
    request: Request,
    tenant_id: str,
    full_path: str,
    authz: SpiceDBClient = Depends(get_authz_client),
) -> JSONResponse:
    return await _ext_authz_check_impl(
        request, full_path, authz, trusted_tenant=tenant_id,
    )


@router.api_route("/t/{tenant_id}/check-oidc", methods=_ALL_METHODS)
@router.api_route("/t/{tenant_id}/check-oidc/", methods=_ALL_METHODS)
async def ext_authz_check_oidc_tenant(
    request: Request,
    tenant_id: str,
    authz: SpiceDBClient = Depends(get_authz_client),
) -> JSONResponse:
    return await _ext_authz_check_impl(
        request, "", authz, oidc_mode=True, trusted_tenant=tenant_id,
    )


@router.api_route("/t/{tenant_id}/check-oidc/{full_path:path}", methods=_ALL_METHODS)
async def ext_authz_check_oidc_tenant_with_path(
    request: Request,
    tenant_id: str,
    full_path: str,
    authz: SpiceDBClient = Depends(get_authz_client),
) -> JSONResponse:
    return await _ext_authz_check_impl(
        request, full_path, authz, oidc_mode=True, trusted_tenant=tenant_id,
    )


# ---------------------------------------------------------------------------
# Legacy un-tenanted routes — deprecated; kept for the reconcile window.
# ---------------------------------------------------------------------------

@router.api_route("/check-bootstrap", methods=_ALL_METHODS)
@router.api_route("/check-bootstrap/", methods=_ALL_METHODS)
async def ext_authz_check_bootstrap(
    request: Request,
    authz: SpiceDBClient = Depends(get_authz_client),
) -> JSONResponse:
    return await _ext_authz_check_impl(request, "", authz)


@router.api_route("/check-bootstrap/{full_path:path}", methods=_ALL_METHODS)
async def ext_authz_check_bootstrap_with_path(
    request: Request,
    full_path: str,
    authz: SpiceDBClient = Depends(get_authz_client),
) -> JSONResponse:
    return await _ext_authz_check_impl(request, full_path, authz)


@router.api_route("/check-oidc", methods=_ALL_METHODS)
@router.api_route("/check-oidc/", methods=_ALL_METHODS)
async def ext_authz_check_oidc(
    request: Request,
    authz: SpiceDBClient = Depends(get_authz_client),
) -> JSONResponse:
    return await _ext_authz_check_impl(request, "", authz, oidc_mode=True)


@router.api_route("/check-oidc/{full_path:path}", methods=_ALL_METHODS)
async def ext_authz_check_oidc_with_path(
    request: Request,
    full_path: str,
    authz: SpiceDBClient = Depends(get_authz_client),
) -> JSONResponse:
    return await _ext_authz_check_impl(request, full_path, authz, oidc_mode=True)


# Legacy /check routes kept for backwards compatibility.
@router.api_route("/check", methods=_ALL_METHODS)
async def ext_authz_check(
    request: Request,
    authz: SpiceDBClient = Depends(get_authz_client),
) -> JSONResponse:
    return await _ext_authz_check_impl(request, "", authz, oidc_mode=True)


@router.api_route("/check/{full_path:path}", methods=_ALL_METHODS)
async def ext_authz_check_with_path(
    request: Request,
    full_path: str,
    authz: SpiceDBClient = Depends(get_authz_client),
) -> JSONResponse:
    return await _ext_authz_check_impl(request, full_path, authz, oidc_mode=True)
