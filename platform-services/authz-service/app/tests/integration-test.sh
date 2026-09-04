#!/bin/bash
#
# authz-service Integration Tests
#
# Tests the core authorization flow against a running authz-service deployment:
#   1. Grant a relationship as cortex-owner → 200 + ZedToken
#   2. Check the granted relationship → 200 + allowed=true
#   3. Check a non-existent relationship → 200 + allowed=false
#   4. Revoke the relationship as cortex-owner → 200 + ZedToken
#   5. Check after revocation → 200 + allowed=false
#   6. Grant as cortex-user (not owner) → 403
#   7. Health check → 200
#
# Endpoints exercised:
#   POST /v1/permissions/grant
#   POST /v1/permissions/check
#   POST /v1/permissions/revoke
#   GET  /health
#
# Required headers on every permissions call:
#   x-cortex-tenant  — tenant identifier
#   x-cortex-sub     — caller subject (e.g. service-account or user id)
#   x-cortex-roles   — comma-separated roles (cortex-owner or cortex-user)
#
# Resource format : <type>:<id>@<tenant_id>   e.g. document:doc-1@tenant-acme
# Subject format  : <type>:<id>               e.g. user:alice
#
# Environment:
#   AUTHZ_SERVICE_URL — base URL of the service (default: http://localhost:8080)
#
# Usage:
#   # Local (port-forward)
#   kubectl port-forward -n cortex-authz svc/authz-service 8080:8080
#   AUTHZ_SERVICE_URL=http://localhost:8080 ./services/authz-service/tests/integration-test.sh
#
#   # Azure (ingress)
#   AUTHZ_SERVICE_URL=https://authz.cortex.example.com ./services/authz-service/tests/integration-test.sh

set -euo pipefail

# ── Configuration ──────────────────────────────────────────────────────────────
AUTHZ_SERVICE_URL="${AUTHZ_SERVICE_URL:-http://localhost:8080}"

# Test fixtures — deterministic identifiers scoped to this test run.
TENANT_ID="tenant-integration-test"
OWNER_SUB="svc:test-runner"
USER_SUB="svc:test-user"
RESOURCE="document:doc-integration-1@${TENANT_ID}"
RELATION="viewer"
PERMISSION="view"
GRANTED_SUBJECT="user:alice-integration"
OTHER_SUBJECT="user:bob-integration"

FAILED=0
GRANT_TOKEN=""

# ── Colours (disabled when stdout is not a terminal) ──────────────────────────
if [ -t 1 ]; then
    GREEN="\033[0;32m"
    RED="\033[0;31m"
    YELLOW="\033[0;33m"
    CYAN="\033[0;36m"
    RESET="\033[0m"
else
    GREEN=""
    RED=""
    YELLOW=""
    CYAN=""
    RESET=""
fi

pass() { printf "${GREEN}  PASS${RESET}  %s\n" "$1"; }
fail() { printf "${RED}  FAIL${RESET}  %s\n" "$1"; FAILED=1; }
info() { printf "${YELLOW}  INFO${RESET}  %s\n" "$1"; }
section() { printf "\n${CYAN}-- %s${RESET}\n" "$1"; }

# ── HTTP helper ────────────────────────────────────────────────────────────────
# http_post <path> <headers_args...> -- <body>
# Writes HTTP status code to stdout and full response body to LAST_BODY.
LAST_BODY=""
http_post() {
    local path="$1"
    shift
    local extra_headers=()
    local body=""
    local parsing_body=0
    for arg in "$@"; do
        if [ "$arg" = "--" ]; then
            parsing_body=1
        elif [ "$parsing_body" -eq 1 ]; then
            body="$arg"
        else
            extra_headers+=("$arg")
        fi
    done

    local tmp
    tmp=$(mktemp)
    local status
    status=$(curl -s -o "$tmp" -w "%{http_code}" \
        -X POST "${AUTHZ_SERVICE_URL}${path}" \
        -H "Content-Type: application/json" \
        "${extra_headers[@]}" \
        -d "$body")
    LAST_BODY=$(cat "$tmp")
    rm -f "$tmp"
    echo "$status"
}

http_get() {
    local path="$1"
    local tmp
    tmp=$(mktemp)
    local status
    status=$(curl -s -o "$tmp" -w "%{http_code}" \
        -X GET "${AUTHZ_SERVICE_URL}${path}")
    LAST_BODY=$(cat "$tmp")
    rm -f "$tmp"
    echo "$status"
}

# Extract a JSON field value (no jq dependency — pure bash + grep).
# Usage: json_field <key> <json>
json_field() {
    local key="$1"
    local json="$2"
    echo "$json" | grep -o "\"${key}\":\"[^\"]*\"" | head -1 | cut -d'"' -f4
}

json_bool() {
    local key="$1"
    local json="$2"
    echo "$json" | grep -o "\"${key}\":[^,}]*" | head -1 | cut -d':' -f2 | tr -d ' '
}

# ── Banner ─────────────────────────────────────────────────────────────────────
echo ""
echo "=============================================="
echo " authz-service Integration Tests"
echo "=============================================="
printf " Service : %s\n" "$AUTHZ_SERVICE_URL"
printf " Tenant  : %s\n" "$TENANT_ID"
printf " Resource: %s\n" "$RESOURCE"
echo ""

# ── Test 7: Health check ───────────────────────────────────────────────────────
# Run health first so we fail fast if the service is unreachable.
section "Health"

STATUS=$(http_get "/health")
if [ "$STATUS" = "200" ]; then
    pass "GET /health → 200 ok"
else
    fail "GET /health — expected 200, got ${STATUS} (is the service running at ${AUTHZ_SERVICE_URL}?)"
    echo ""
    printf "${RED}Service unreachable — aborting remaining tests.${RESET}\n"
    exit 1
fi

# ── Test 1: Grant relationship as cortex-owner ─────────────────────────────────
section "Grant (cortex-owner)"

GRANT_BODY=$(printf '{"resource":"%s","relation":"%s","subject":"%s"}' \
    "$RESOURCE" "$RELATION" "$GRANTED_SUBJECT")

STATUS=$(http_post "/v1/permissions/grant" \
    -H "x-cortex-tenant: ${TENANT_ID}" \
    -H "x-cortex-sub: ${OWNER_SUB}" \
    -H "x-cortex-roles: cortex-owner" \
    -- "$GRANT_BODY")

if [ "$STATUS" = "200" ]; then
    GRANT_TOKEN=$(json_field "granted_at" "$LAST_BODY")
    if [ -n "$GRANT_TOKEN" ]; then
        pass "POST /v1/permissions/grant → 200, ZedToken present (${GRANT_TOKEN})"
    else
        fail "POST /v1/permissions/grant → 200 but granted_at missing from response body: ${LAST_BODY}"
    fi
else
    fail "POST /v1/permissions/grant — expected 200, got ${STATUS} | body: ${LAST_BODY}"
fi

# ── Test 2: Check granted relationship → allowed=true ─────────────────────────
section "Check (granted subject — expect allowed=true)"

CHECK_BODY=$(printf '{"resource":"%s","permission":"%s","subject":"%s"}' \
    "$RESOURCE" "$PERMISSION" "$GRANTED_SUBJECT")

# Thread the ZedToken from the grant for new-enemy protection when available.
if [ -n "$GRANT_TOKEN" ]; then
    CHECK_BODY=$(printf '{"resource":"%s","permission":"%s","subject":"%s","consistency_token":"%s"}' \
        "$RESOURCE" "$PERMISSION" "$GRANTED_SUBJECT" "$GRANT_TOKEN")
fi

STATUS=$(http_post "/v1/permissions/check" \
    -H "x-cortex-tenant: ${TENANT_ID}" \
    -H "x-cortex-sub: ${OWNER_SUB}" \
    -H "x-cortex-roles: cortex-user" \
    -- "$CHECK_BODY")

if [ "$STATUS" = "200" ]; then
    ALLOWED=$(json_bool "allowed" "$LAST_BODY")
    if [ "$ALLOWED" = "true" ]; then
        pass "POST /v1/permissions/check → 200, allowed=true for granted subject"
    else
        fail "POST /v1/permissions/check — expected allowed=true, got allowed=${ALLOWED} | body: ${LAST_BODY}"
    fi
else
    fail "POST /v1/permissions/check — expected 200, got ${STATUS} | body: ${LAST_BODY}"
fi

# ── Test 3: Check non-existent relationship → allowed=false ───────────────────
section "Check (different subject — expect allowed=false)"

CHECK_BODY_OTHER=$(printf '{"resource":"%s","permission":"%s","subject":"%s"}' \
    "$RESOURCE" "$PERMISSION" "$OTHER_SUBJECT")

STATUS=$(http_post "/v1/permissions/check" \
    -H "x-cortex-tenant: ${TENANT_ID}" \
    -H "x-cortex-sub: ${OWNER_SUB}" \
    -H "x-cortex-roles: cortex-user" \
    -- "$CHECK_BODY_OTHER")

if [ "$STATUS" = "200" ]; then
    ALLOWED=$(json_bool "allowed" "$LAST_BODY")
    if [ "$ALLOWED" = "false" ]; then
        pass "POST /v1/permissions/check → 200, allowed=false for subject with no grant"
    else
        fail "POST /v1/permissions/check — expected allowed=false for ungrant subject, got allowed=${ALLOWED} | body: ${LAST_BODY}"
    fi
else
    fail "POST /v1/permissions/check — expected 200, got ${STATUS} | body: ${LAST_BODY}"
fi

# ── Test 4: Revoke relationship as cortex-owner ────────────────────────────────
section "Revoke (cortex-owner)"

REVOKE_BODY=$(printf '{"resource":"%s","relation":"%s","subject":"%s"}' \
    "$RESOURCE" "$RELATION" "$GRANTED_SUBJECT")

STATUS=$(http_post "/v1/permissions/revoke" \
    -H "x-cortex-tenant: ${TENANT_ID}" \
    -H "x-cortex-sub: ${OWNER_SUB}" \
    -H "x-cortex-roles: cortex-owner" \
    -- "$REVOKE_BODY")

if [ "$STATUS" = "200" ]; then
    REVOKE_TOKEN=$(json_field "revoked_at" "$LAST_BODY")
    if [ -n "$REVOKE_TOKEN" ]; then
        pass "POST /v1/permissions/revoke → 200, ZedToken present (${REVOKE_TOKEN})"
    else
        fail "POST /v1/permissions/revoke → 200 but revoked_at missing from response body: ${LAST_BODY}"
    fi
else
    fail "POST /v1/permissions/revoke — expected 200, got ${STATUS} | body: ${LAST_BODY}"
fi

# ── Test 5: Check after revocation → allowed=false ────────────────────────────
section "Check (after revocation — expect allowed=false)"

REVOKE_TOKEN="${REVOKE_TOKEN:-}"
POST_REVOKE_BODY=$(printf '{"resource":"%s","permission":"%s","subject":"%s"}' \
    "$RESOURCE" "$PERMISSION" "$GRANTED_SUBJECT")

if [ -n "$REVOKE_TOKEN" ]; then
    POST_REVOKE_BODY=$(printf '{"resource":"%s","permission":"%s","subject":"%s","consistency_token":"%s"}' \
        "$RESOURCE" "$PERMISSION" "$GRANTED_SUBJECT" "$REVOKE_TOKEN")
fi

STATUS=$(http_post "/v1/permissions/check" \
    -H "x-cortex-tenant: ${TENANT_ID}" \
    -H "x-cortex-sub: ${OWNER_SUB}" \
    -H "x-cortex-roles: cortex-user" \
    -- "$POST_REVOKE_BODY")

if [ "$STATUS" = "200" ]; then
    ALLOWED=$(json_bool "allowed" "$LAST_BODY")
    if [ "$ALLOWED" = "false" ]; then
        pass "POST /v1/permissions/check → 200, allowed=false after revocation"
    else
        fail "POST /v1/permissions/check — expected allowed=false after revocation, got allowed=${ALLOWED} | body: ${LAST_BODY}"
    fi
else
    fail "POST /v1/permissions/check — expected 200, got ${STATUS} | body: ${LAST_BODY}"
fi

# ── Test 6: Grant as cortex-user → 403 ────────────────────────────────────────
section "Grant (cortex-user only — expect 403)"

GRANT_USER_BODY=$(printf '{"resource":"%s","relation":"%s","subject":"%s"}' \
    "$RESOURCE" "$RELATION" "$GRANTED_SUBJECT")

STATUS=$(http_post "/v1/permissions/grant" \
    -H "x-cortex-tenant: ${TENANT_ID}" \
    -H "x-cortex-sub: ${USER_SUB}" \
    -H "x-cortex-roles: cortex-user" \
    -- "$GRANT_USER_BODY")

if [ "$STATUS" = "403" ]; then
    pass "POST /v1/permissions/grant as cortex-user → 403 Forbidden (owner role required)"
else
    fail "POST /v1/permissions/grant as cortex-user — expected 403, got ${STATUS} | body: ${LAST_BODY}"
fi

# ── Cross-tenant isolation probe ───────────────────────────────────────────────
# ADR mandatory: a resource scoped to tenant-A must be rejected when the
# caller's x-cortex-tenant header carries tenant-B.
section "Cross-tenant isolation (expect 403)"

FOREIGN_RESOURCE="document:doc-integration-1@tenant-other"
CROSS_TENANT_BODY=$(printf '{"resource":"%s","permission":"%s","subject":"%s"}' \
    "$FOREIGN_RESOURCE" "$PERMISSION" "$GRANTED_SUBJECT")

STATUS=$(http_post "/v1/permissions/check" \
    -H "x-cortex-tenant: ${TENANT_ID}" \
    -H "x-cortex-sub: ${OWNER_SUB}" \
    -H "x-cortex-roles: cortex-user" \
    -- "$CROSS_TENANT_BODY")

if [ "$STATUS" = "403" ]; then
    pass "POST /v1/permissions/check with foreign-tenant resource → 403 (tenant isolation enforced)"
else
    fail "POST /v1/permissions/check with foreign-tenant resource — expected 403, got ${STATUS} | body: ${LAST_BODY}"
fi

# ── Missing required header → 401 ─────────────────────────────────────────────
section "Missing required headers (expect 401)"

# Missing x-cortex-tenant
STATUS=$(http_post "/v1/permissions/check" \
    -H "x-cortex-sub: ${OWNER_SUB}" \
    -H "x-cortex-roles: cortex-user" \
    -- "$CHECK_BODY")

if [ "$STATUS" = "401" ]; then
    pass "POST /v1/permissions/check without x-cortex-tenant → 401"
else
    fail "POST /v1/permissions/check without x-cortex-tenant — expected 401, got ${STATUS} | body: ${LAST_BODY}"
fi

# Missing x-cortex-sub
STATUS=$(http_post "/v1/permissions/check" \
    -H "x-cortex-tenant: ${TENANT_ID}" \
    -H "x-cortex-roles: cortex-user" \
    -- "$CHECK_BODY")

if [ "$STATUS" = "401" ]; then
    pass "POST /v1/permissions/check without x-cortex-sub → 401"
else
    fail "POST /v1/permissions/check without x-cortex-sub — expected 401, got ${STATUS} | body: ${LAST_BODY}"
fi

# Missing x-cortex-roles
STATUS=$(http_post "/v1/permissions/check" \
    -H "x-cortex-tenant: ${TENANT_ID}" \
    -H "x-cortex-sub: ${OWNER_SUB}" \
    -- "$CHECK_BODY")

if [ "$STATUS" = "401" ]; then
    pass "POST /v1/permissions/check without x-cortex-roles → 401"
else
    fail "POST /v1/permissions/check without x-cortex-roles — expected 401, got ${STATUS} | body: ${LAST_BODY}"
fi

# ── Summary ────────────────────────────────────────────────────────────────────
echo ""
echo "=============================================="
if [ $FAILED -eq 0 ]; then
    printf "${GREEN}All authz-service integration tests passed${RESET}\n"
    echo ""
    echo "Core authorization flow verified:"
    echo "  - Grant creates a relationship (owner-only)"
    echo "  - Check returns allowed=true for a granted subject"
    echo "  - Check returns allowed=false for an ungrant subject"
    echo "  - Revoke deletes the relationship (owner-only)"
    echo "  - Check returns allowed=false after revocation"
    echo "  - Grant by cortex-user (not owner) is rejected with 403"
    echo "  - Cross-tenant resource access is rejected with 403"
    echo "  - Missing identity headers are rejected with 401"
    exit 0
else
    printf "${RED}Some authz-service integration tests failed${RESET}\n"
    exit 1
fi
