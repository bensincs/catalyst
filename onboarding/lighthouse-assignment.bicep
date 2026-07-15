// Cortex — Lighthouse registration assignment (resource-group scope).
// Deployed as a module from lighthouse-delegation.bicep so the delegation binds
// to a single resource group instead of the whole subscription.

targetScope = 'resourceGroup'

@description('Resource id of the registrationDefinition created at subscription scope.')
param registrationDefinitionId string

resource assignment 'Microsoft.ManagedServices/registrationAssignments@2022-10-01' = {
  name: guid(registrationDefinitionId, resourceGroup().id)
  properties: {
    registrationDefinitionId: registrationDefinitionId
  }
}
