@description('Name of the platform ACS Communication Service (in this module\'s target resource group).')
param acsName string

@description('Principal ID of the identity to grant the email-sender role.')
param principalId string

@description('Built-in "Communication and Email Service Owner" role definition ID.')
var communicationAndEmailServiceOwnerRoleId = '09976791-48a7-449e-bb21-39d1a415f350'

resource acs 'Microsoft.Communication/communicationServices@2023-04-01' existing = {
  name: acsName
}

resource ra 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(acs.id, principalId, communicationAndEmailServiceOwnerRoleId)
  scope: acs
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', communicationAndEmailServiceOwnerRoleId)
    principalId: principalId
    principalType: 'ServicePrincipal'
  }
}
