#!/usr/bin/env bash
# Cortex — Lighthouse cross-tenant deploy spike (VERIFICATION, not product code).
#
# Proves the core claim behind the "control plane provisions infra via Lighthouse"
# design: a principal authenticated in the MANAGING (platform) tenant can deploy an
# ARM/Bicep resource into a CUSTOMER's delegated resource group and read its
# outputs — WITHOUT ever authenticating to the customer tenant.
#
# Prereqs:
#   1. As the CUSTOMER admin, deploy the delegation (creates cortex-infra + delegates it):
#        az deployment sub create --subscription <CUSTOMER_SUB> --location <region> \
#          --template-file onboarding/lighthouse-delegation.bicep \
#          --parameters controlPlaneTenantId=<PLATFORM_TENANT> controlPlanePrincipalId=<PLATFORM_SP_OBJECT_ID>
#   2. Sign in here as the MANAGING principal (the platform SP or a managing-tenant user):
#        az login --service-principal -u <appId> -p <secret> --tenant <PLATFORM_TENANT>
#      (or `az login` as a managing-tenant user that holds the delegation).
#
# Usage:
#   ./onboarding/verify-crosstenant-deploy.sh <CUSTOMER_SUBSCRIPTION_ID> [RESOURCE_GROUP] [REGION]
set -euo pipefail

SUB="${1:?usage: verify-crosstenant-deploy.sh <CUSTOMER_SUB> [rg] [region]}"
RG="${2:-cortex-infra}"
REGION="${3:-uksouth}"
MODULE="br:mcr.microsoft.com/bicep/avm/res/storage/storage-account:0.32.1"
STG="cortexlh$(date +%s | tail -c 7)"   # globally-unique-ish, lowercase, <=24 chars
DEPLOY="cortex-lighthouse-spike-$(date +%s)"

echo "▸ Managing identity in use:"
az account show --query '{user:user.name,type:user.type,tenantId:tenantId}' -o jsonc

echo "▸ Is ${SUB} delegated to this managing tenant? (expect a managedByTenants entry)"
if az account list --query "[?id=='${SUB}'].{name:name,homeTenantId:homeTenantId,managedByTenants:managedByTenants}" -o jsonc | grep -q managedByTenants; then
  az account list --query "[?id=='${SUB}'].managedByTenants" -o jsonc
else
  echo "  ⚠️  No managedByTenants shown. Run 'az account clear && az login ...' to refresh, or the delegation isn't in place yet."
fi

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
cat > "$TMP/spike.bicep" <<EOF
targetScope = 'resourceGroup'
param location string = resourceGroup().location
module infra '${MODULE}' = {
  name: 'cortex-spike'
  params: {
    name: '${STG}'
    location: location
  }
}
output storageName string = infra.outputs.name
output resourceId string = infra.outputs.resourceId
output primaryBlobEndpoint string = infra.outputs.primaryBlobEndpoint
EOF

echo "▸ Deploying ${MODULE} into ${SUB}/${RG} as the MANAGING principal…"
az deployment group create \
  --subscription "$SUB" \
  --resource-group "$RG" \
  --name "$DEPLOY" \
  --template-file "$TMP/spike.bicep" \
  --parameters location="$REGION" \
  -o none

echo "▸ Deployment outputs (this is what the control plane would wire into Helm values):"
az deployment group show --subscription "$SUB" -g "$RG" -n "$DEPLOY" \
  --query 'properties.outputs' -o jsonc

STATE="$(az deployment group show --subscription "$SUB" -g "$RG" -n "$DEPLOY" --query properties.provisioningState -o tsv)"
echo
if [[ "$STATE" == "Succeeded" ]]; then
  echo "✅ PASS — cross-tenant deploy Succeeded using only managing-tenant credentials."
  echo "   The design holds: the control plane can provision app infra via Lighthouse."
  echo "   (Clean up: az resource delete --ids <resourceId above>, or delete the RG.)"
else
  echo "❌ Deployment state: ${STATE} — inspect the delegation (Contributor on ${RG}) and the SP object id."
  exit 1
fi
