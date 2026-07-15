// Cortex — Azure Lighthouse delegation for control-plane infrastructure management.
//
// The SECOND half of tenant onboarding (the first is main.bicep, which installs
// the reconciler + Foundry into a resource group). The customer deploys this ONCE
// at SUBSCRIPTION scope. It:
//   • creates a dedicated `cortex-infra` resource group, and
//   • delegates JUST that resource group to the Cortex control-plane service
//     principal in the managing (platform) tenant, granting built-in Contributor.
//
// After this, the Cortex control plane deploys each deployment's Bicep/ARM infra
// into cortex-infra cross-tenant (Azure Lighthouse) — the tenant never has to run
// those deployments itself, and the reconciler stays focused on the Foundry data
// plane. Least privilege: the platform can only touch this one resource group.
//
// Lighthouse authorizations must use BUILT-IN roles (no Owner, no DataActions, no
// custom roles), so the grant is Contributor scoped to cortex-infra.
//
//   az deployment sub create \
//     --subscription <YOUR_SUBSCRIPTION> --location <region> \
//     --template-file onboarding/lighthouse-delegation.bicep
//
// The two platform values below are published by Cortex and prefilled — a tenant
// admin normally just accepts them.

targetScope = 'subscription'

@description('Cortex (managing) Entra tenant id — the platform tenant the delegation grants access to. Published by Cortex.')
param controlPlaneTenantId string

@description('Object id of the Cortex control-plane service principal in the Cortex tenant. Published by Cortex.')
param controlPlanePrincipalId string

@description('Azure region for the infra resource group.')
param location string = deployment().location

@description('Resource group Cortex manages for your application infrastructure. Keep it dedicated to Cortex.')
param infraResourceGroupName string = 'cortex-infra'

@description('Display name shown to you under Service providers → Delegations.')
param offerName string = 'Cortex application infrastructure'

// Built-in Contributor. Lighthouse does not allow custom roles / Owner / DataActions.
var contributorRoleId = 'b24988ac-6180-42a0-ab88-20f7382dd24c'

// The dedicated resource group Cortex deploys application infra into.
resource infraRg 'Microsoft.Resources/resourceGroups@2024-03-01' = {
  name: infraResourceGroupName
  location: location
}

// Register the Cortex platform tenant + control-plane SP as an authorized manager.
resource registration 'Microsoft.ManagedServices/registrationDefinitions@2022-10-01' = {
  name: guid(offerName, controlPlaneTenantId, controlPlanePrincipalId)
  properties: {
    registrationDefinitionName: offerName
    description: 'Lets the Cortex control plane deploy application infrastructure into ${infraResourceGroupName}.'
    managedByTenantId: controlPlaneTenantId
    authorizations: [
      {
        principalId: controlPlanePrincipalId
        principalIdDisplayName: 'Cortex control plane'
        roleDefinitionId: contributorRoleId
      }
    ]
  }
}

// Bind the delegation to the cortex-infra resource group only (least blast radius).
module assignment 'lighthouse-assignment.bicep' = {
  name: 'cortex-lighthouse-assignment'
  scope: resourceGroup(infraResourceGroupName)
  params: {
    registrationDefinitionId: registration.id
  }
  dependsOn: [ infraRg ]
}

@description('Give this to Cortex if asked — the resource group the control plane will deploy infra into.')
output infraResourceGroup string = infraResourceGroupName
output registrationDefinitionId string = registration.id
