#!/bin/bash
#
# AuthZ Service Manifest Validation
# Validates that all kustomize overlays build correctly and required resources exist.
#

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
MANIFESTS_DIR="$REPO_ROOT/services/authz-service/manifests"
GITOPS_LOCAL="$REPO_ROOT/gitops/components/authz-service/local"
GITOPS_AZURE="$REPO_ROOT/gitops/components/authz-service/azure"
# The schema-load Job mounts schema/cortex.zed via the spicedb-schema
# ConfigMap built by kustomize from this source file.
SCHEMA_CONFIGMAP="$REPO_ROOT/services/authz-service/schema/cortex.zed"

echo "=== AuthZ Service Manifest Validation ==="
echo ""

FAILED=0

# ---------------------------------------------------------------------------
# 1. Service manifests base build
# ---------------------------------------------------------------------------
echo "Validating service manifests base build..."
if [ ! -d "$MANIFESTS_DIR" ]; then
    echo "  FAIL service manifests directory missing: $MANIFESTS_DIR"
    FAILED=1
elif kubectl kustomize "$MANIFESTS_DIR" > /dev/null 2>&1; then
    echo "  PASS service manifests build successfully"
else
    echo "  FAIL service manifests failed to build"
    kubectl kustomize "$MANIFESTS_DIR" 2>&1 | sed 's/^/       /'
    FAILED=1
fi

# ---------------------------------------------------------------------------
# 2. Local gitops overlay build
# ---------------------------------------------------------------------------
echo ""
echo "Validating local gitops overlay..."
if kubectl kustomize "$GITOPS_LOCAL" > /dev/null 2>&1; then
    echo "  PASS local overlay builds successfully"
else
    echo "  FAIL local overlay failed to build"
    kubectl kustomize "$GITOPS_LOCAL" 2>&1 | sed 's/^/       /'
    FAILED=1
fi

# ---------------------------------------------------------------------------
# 3. Azure gitops overlay build
# ---------------------------------------------------------------------------
echo ""
echo "Validating azure gitops overlay..."
if kubectl kustomize "$GITOPS_AZURE" > /dev/null 2>&1; then
    echo "  PASS azure overlay builds successfully"
else
    echo "  FAIL azure overlay failed to build"
    kubectl kustomize "$GITOPS_AZURE" 2>&1 | sed 's/^/       /'
    FAILED=1
fi

# ---------------------------------------------------------------------------
# 4. Required resources in local overlay
# ---------------------------------------------------------------------------
echo ""
echo "Checking required resources in local overlay..."

LOCAL_MANIFEST=""
if kubectl kustomize "$GITOPS_LOCAL" > /dev/null 2>&1; then
    LOCAL_MANIFEST=$(kubectl kustomize "$GITOPS_LOCAL")
fi

check_resource() {
    local kind=$1
    local name=$2
    if [ -z "$LOCAL_MANIFEST" ]; then
        echo "  FAIL $kind/$name (overlay did not build)"
        return 1
    fi
    if echo "$LOCAL_MANIFEST" | grep -q "kind: $kind" && echo "$LOCAL_MANIFEST" | grep -q "name: $name"; then
        echo "  PASS $kind/$name exists"
        return 0
    else
        echo "  FAIL $kind/$name missing"
        return 1
    fi
}

check_resource "Deployment"     "authz-service"       || FAILED=1
check_resource "Deployment"     "spicedb"             || FAILED=1
check_resource "Service"        "authz-service"       || FAILED=1
check_resource "Service"        "spicedb"             || FAILED=1
check_resource "ConfigMap"      "spicedb-config"      || FAILED=1
check_resource "ConfigMap"      "spicedb-schema"      || FAILED=1
check_resource "Job"            "spicedb-schema-load" || FAILED=1
check_resource "NetworkPolicy"  "default-deny-all"    || FAILED=1

# ---------------------------------------------------------------------------
# 5. SpiceDB schema ConfigMap contains valid schema content
# ---------------------------------------------------------------------------
echo ""
echo "Checking SpiceDB schema content..."

if [ ! -f "$SCHEMA_CONFIGMAP" ]; then
    echo "  FAIL schema ConfigMap file missing: $SCHEMA_CONFIGMAP"
    FAILED=1
else
    SCHEMA_VALID=0

    if grep -q "definition tenant"      "$SCHEMA_CONFIGMAP"; then
        echo "  PASS schema defines 'tenant' type"
    else
        echo "  FAIL schema missing 'definition tenant'"
        SCHEMA_VALID=1
        FAILED=1
    fi

    if grep -q "definition user"        "$SCHEMA_CONFIGMAP"; then
        echo "  PASS schema defines 'user' type"
    else
        echo "  FAIL schema missing 'definition user'"
        SCHEMA_VALID=1
        FAILED=1
    fi

    if grep -q "definition application" "$SCHEMA_CONFIGMAP"; then
        echo "  PASS schema defines 'application' type"
    else
        echo "  FAIL schema missing 'definition application'"
        SCHEMA_VALID=1
        FAILED=1
    fi

    if grep -q "definition agent"       "$SCHEMA_CONFIGMAP"; then
        echo "  PASS schema defines 'agent' type"
    else
        echo "  FAIL schema missing 'definition agent'"
        SCHEMA_VALID=1
        FAILED=1
    fi

    if grep -q "permission"             "$SCHEMA_CONFIGMAP"; then
        echo "  PASS schema contains permission declarations"
    else
        echo "  FAIL schema missing permission declarations"
        SCHEMA_VALID=1
        FAILED=1
    fi

    if grep -q "relation"               "$SCHEMA_CONFIGMAP"; then
        echo "  PASS schema contains relation declarations"
    else
        echo "  FAIL schema missing relation declarations"
        SCHEMA_VALID=1
        FAILED=1
    fi

    if [ $SCHEMA_VALID -eq 0 ]; then
        echo "  PASS schema.zed content is structurally valid"
    fi
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
if [ $FAILED -eq 0 ]; then
    echo "=== All validations passed PASS ==="
    exit 0
else
    echo "=== Some validations failed FAIL ==="
    exit 1
fi
