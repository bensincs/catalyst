// Grants the control-plane managed identity the roles it needs to provision
// PLATFORM-HOSTED tenants into the platform's OWN subscription (there is no Azure
// Lighthouse delegation to itself). Deployed as a subscription-scoped module from
// the RG-scoped control-plane deployment.
//
//   • Contributor — create the per-tenant resource groups, reconciler managed
//     identities, Foundry accounts/projects, AKS clusters, container apps, etc.
//   • User Access Administrator — the footprint assigns AKS/Foundry/Network roles
//     to each tenant's reconciler identity; without Lighthouse's scoped
//     delegation, the control plane needs to create those role assignments itself.
//
// The principal deploying the control plane needs Owner (or User Access
// Administrator) on this subscription to create these assignments.
targetScope = 'subscription'

@description('Control-plane managed identity principal (object) id — the grantee.')
param controlPlanePrincipalId string

var contributorRoleId = 'b24988ac-6180-42a0-ab88-20f7382dd24c'
var userAccessAdminRoleId = '18d7d88d-d35e-4fb5-a5c3-7773c20a72d9'

resource contributor 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, controlPlanePrincipalId, contributorRoleId)
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', contributorRoleId)
    principalId: controlPlanePrincipalId
    principalType: 'ServicePrincipal'
  }
}

resource userAccessAdmin 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, controlPlanePrincipalId, userAccessAdminRoleId)
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', userAccessAdminRoleId)
    principalId: controlPlanePrincipalId
    principalType: 'ServicePrincipal'
  }
}
