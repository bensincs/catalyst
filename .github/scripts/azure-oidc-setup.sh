#!/usr/bin/env bash
#
# One-time setup: create the Entra app + federated credentials GitHub Actions
# uses to deploy to Azure without any stored password (OIDC), grant it the roles
# it needs, and (if `gh` is present) set the three repo secrets.
#
# Requires an operator who can create app registrations in the tenant and assign
# roles on the subscription. Re-runnable (idempotent).
#
#   ./.github/scripts/azure-oidc-setup.sh [owner/repo]
#
# Overridable via env: APP_NAME, AZURE_SUBSCRIPTION_ID, AZURE_RESOURCE_GROUP,
# ACR_NAME, PUBLIC_ACR_NAME, BRANCHES (space-separated).
set -euo pipefail

REPO="${1:-bensincs/catalyst}"
APP_NAME="${APP_NAME:-cortex-github-oidc}"
SUBSCRIPTION="${AZURE_SUBSCRIPTION_ID:-dd78ec54-2f00-41fc-8055-8c1f2ad66a1d}"
RG="${AZURE_RESOURCE_GROUP:-cortex-control-plane}"
ACR_NAME="${ACR_NAME:-cortexcpacrzo7yflmq}"
PUBLIC_ACR_NAME="${PUBLIC_ACR_NAME:-cortexpubliczo7yflmq}"
read -r -a BRANCHES <<<"${BRANCHES:-main feat/k8s-gitops-mesh}"

echo "Repo:         $REPO"
echo "App:          $APP_NAME"
echo "Subscription: $SUBSCRIPTION"
echo "Resource grp: $RG"
echo "Registries:   $ACR_NAME, $PUBLIC_ACR_NAME"
echo "Branches:     ${BRANCHES[*]}"
echo

az account set --subscription "$SUBSCRIPTION"
TENANT_ID="$(az account show --query tenantId -o tsv)"

# 1) App registration + service principal ------------------------------------
APP_ID="$(az ad app list --display-name "$APP_NAME" --query "[0].appId" -o tsv)"
if [ -z "$APP_ID" ]; then
  echo "Creating app registration '$APP_NAME'…"
  APP_ID="$(az ad app create --display-name "$APP_NAME" --query appId -o tsv)"
fi
az ad sp show --id "$APP_ID" >/dev/null 2>&1 || az ad sp create --id "$APP_ID" >/dev/null
SP_OID="$(az ad sp show --id "$APP_ID" --query id -o tsv)"
echo "App (client) id: $APP_ID"

# 2) Federated credentials — one per deployable branch, plus workflow_dispatch -
add_fic() {
  local name="$1" subject="$2"
  if [ -z "$(az ad app federated-credential list --id "$APP_ID" --query "[?subject=='$subject'] | [0].name" -o tsv)" ]; then
    az ad app federated-credential create --id "$APP_ID" --parameters "{
      \"name\": \"$name\",
      \"issuer\": \"https://token.actions.githubusercontent.com\",
      \"subject\": \"$subject\",
      \"audiences\": [\"api://AzureADTokenExchange\"]
    }" >/dev/null
    echo "  + federated credential: $subject"
  fi
}
for b in "${BRANCHES[@]}"; do
  add_fic "gh-branch-$(echo "$b" | tr '/' '-')" "repo:${REPO}:ref:refs/heads/${b}"
done

# 3) RBAC — Contributor on the RG (Container Apps) + each registry (acr build/import)
assign() {
  az role assignment create --assignee-object-id "$SP_OID" --assignee-principal-type ServicePrincipal \
    --role "Contributor" --scope "$1" >/dev/null 2>&1 || true
  echo "  + Contributor: $1"
}
assign "/subscriptions/$SUBSCRIPTION/resourceGroups/$RG"
for acr in "$ACR_NAME" "$PUBLIC_ACR_NAME"; do
  id="$(az acr show -n "$acr" --query id -o tsv 2>/dev/null || true)"
  [ -n "$id" ] && assign "$id"
done

# 4) Repo secrets -------------------------------------------------------------
echo
echo "Set these as GitHub Actions repo secrets:"
echo "  AZURE_CLIENT_ID=$APP_ID"
echo "  AZURE_TENANT_ID=$TENANT_ID"
echo "  AZURE_SUBSCRIPTION_ID=$SUBSCRIPTION"
if command -v gh >/dev/null 2>&1; then
  gh secret set AZURE_CLIENT_ID --repo "$REPO" --body "$APP_ID"
  gh secret set AZURE_TENANT_ID --repo "$REPO" --body "$TENANT_ID"
  gh secret set AZURE_SUBSCRIPTION_ID --repo "$REPO" --body "$SUBSCRIPTION"
  echo "Secrets set on $REPO via gh."
else
  echo "(gh not found — set the three secrets above manually.)"
fi
echo "Done."
