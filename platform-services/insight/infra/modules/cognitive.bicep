@description('Name of the Cognitive Services account.')
param name string

@description('Azure region for the Cognitive Services account.')
param location string

@description('Tags to apply to all resources in this module.')
param tags object = {}

@description('Kind of Cognitive Services account (e.g. SpeechServices, TextTranslation, FormRecognizer).')
param kind string

@description('SKU name for the Cognitive Services account.')
param skuName string

@description('Whether to attach a user-assigned identity in addition to the system-assigned identity.')
param useUserAssignedIdentity bool = false

@description('Resource ID of the user-assigned identity to attach when useUserAssignedIdentity is true.')
param userAssignedIdentityId string = ''

@description('Resource ID of a customer-owned (bring-your-own) storage account for the service. Leave empty to disable.')
param userOwnedStorageAccountId string = ''

@description('Client ID of the identity used to access the customer-owned storage account.')
param userOwnedStorageIdentityClientId string = ''

@description('Resource ID of the subnet used for the private endpoint.')
param peSubnetId string

@description('Resource ID of the privatelink.cognitiveservices.azure.com private DNS zone.')
param dnsZoneId string

@description('Name of the private DNS zone group for the private endpoint.')
param dnsZoneGroupName string

@description('Principal ID of the shared workload user-assigned identity to grant Cognitive Services access.')
param workloadPrincipalId string

@description('Built-in "Cognitive Services User" role definition ID.')
var cognitiveServicesUserRoleId = 'a97b65f3-24c7-4388-baec-2e87135dc908'

@description('Identity block: system-assigned plus optional user-assigned identity.')
var identityConfig = useUserAssignedIdentity ? {
  type: 'SystemAssigned, UserAssigned'
  userAssignedIdentities: {
    '${userAssignedIdentityId}': {}
  }
} : {
  type: 'SystemAssigned'
}

@description('Base properties applied to every Cognitive Services account in this module.')
var baseProperties = {
  customSubDomainName: name
  disableLocalAuth: true
  publicNetworkAccess: 'Disabled'
  restrictOutboundNetworkAccess: true
}

@description('userOwnedStorage property fragment, added only when a storage account ID is supplied.')
var userOwnedStorageProperties = userOwnedStorageAccountId != '' ? {
  userOwnedStorage: [
    {
      resourceId: userOwnedStorageAccountId
      identityClientId: userOwnedStorageIdentityClientId
    }
  ]
} : {}

resource account 'Microsoft.CognitiveServices/accounts@2024-10-01' = {
  name: name
  location: location
  tags: tags
  kind: kind
  sku: {
    name: skuName
  }
  identity: identityConfig
  properties: union(baseProperties, userOwnedStorageProperties)
}

resource pe 'Microsoft.Network/privateEndpoints@2024-05-01' = {
  name: 'pe-${name}'
  location: location
  tags: tags
  properties: {
    subnet: {
      id: peSubnetId
    }
    privateLinkServiceConnections: [
      {
        name: 'psc-${name}'
        properties: {
          privateLinkServiceId: account.id
          groupIds: [
            'account'
          ]
        }
      }
    ]
  }

  resource dns 'privateDnsZoneGroups@2024-05-01' = {
    name: dnsZoneGroupName
    properties: {
      privateDnsZoneConfigs: [
        {
          name: 'config'
          properties: {
            privateDnsZoneId: dnsZoneId
          }
        }
      ]
    }
  }
}

resource ra 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(account.id, workloadPrincipalId, cognitiveServicesUserRoleId)
  scope: account
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', cognitiveServicesUserRoleId)
    principalId: workloadPrincipalId
    principalType: 'ServicePrincipal'
  }
}

@description('Resource ID of the Cognitive Services account.')
output id string = account.id

@description('Name of the Cognitive Services account.')
output name string = account.name

@description('Endpoint of the Cognitive Services account.')
output endpoint string = account.properties.endpoint

@description('Location of the Cognitive Services account.')
output location string = account.location
