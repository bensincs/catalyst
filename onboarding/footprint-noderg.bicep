// Grants the reconciler's managed identity Network Contributor on the AKS node
// (MC_) resource group. The reconciler needs it to open the Application Gateway
// for Containers association subnet's NSG for inbound frontend traffic: the AKS
// add-on gives that subnet a dedicated NSG whose only inbound rules are the
// defaults (ending in DenyAllInBound), which drops client SYNs to the AGC
// data-path proxies that hold the frontend IP — so the public FQDN times out at
// TCP connect until the reconciler adds an inbound allow rule.
//
// Deployed as a module because the node resource group is created by AKS and a
// role assignment must be scoped to it (not the footprint resource group).
targetScope = 'resourceGroup'

@description('Reconciler managed identity principalId (the grantee).')
param reconcilerPrincipalId string

@description('Reconciler managed identity resourceId, for delegatedManagedIdentityResourceId.')
param reconcilerIdentityId string

@description('Cluster name, for a stable role-assignment name.')
param clusterName string

// Network Contributor — read the AGC subnet + write its inbound NSG rule.
var networkContributorRoleId = '4d97b98b-1d4f-4787-a291-c67834d212e7'

resource nsgRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(clusterName, reconcilerIdentityId, networkContributorRoleId)
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', networkContributorRoleId)
    principalId: reconcilerPrincipalId
    principalType: 'ServicePrincipal'
    delegatedManagedIdentityResourceId: reconcilerIdentityId
  }
}
