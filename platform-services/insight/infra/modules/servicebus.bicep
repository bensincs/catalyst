@description('Name of the Service Bus namespace.')
param name string

@description('Azure region for the Service Bus namespace.')
param location string

@description('Tags to apply to all resources in this module.')
param tags object = {}

@description('Messaging units (capacity) for the Premium namespace.')
param capacity int = 1

@description('Names of the queues to create in the namespace.')
param queues array = [
  'meeting-queue'
  'insight-indexer-queue'
]

@description('Resource ID of the customer-managed key user-assigned identity used for encryption.')
param cmkIdentityId string

@description('Name of the customer-managed key in Key Vault.')
param cmkKeyName string

@description('URI of the Key Vault holding the customer-managed key.')
param keyVaultUri string

@description('Resource ID of the subnet used for the private endpoint.')
param peSubnetId string

@description('Resource ID of the privatelink.servicebus.windows.net private DNS zone.')
param dnsZoneId string

@description('Principal ID of the shared workload user-assigned identity to grant queue access.')
param workloadPrincipalId string

@description('Built-in "Azure Service Bus Data Sender" role definition ID.')
var serviceBusDataSenderRoleId = '69a216fc-b8fb-44d8-bc22-1f3c2cd27a39'

@description('Built-in "Azure Service Bus Data Receiver" role definition ID.')
var serviceBusDataReceiverRoleId = '4f6d3b9b-027b-4f4c-9142-0e5a2a2247e0'

resource namespace 'Microsoft.ServiceBus/namespaces@2022-10-01-preview' = {
  name: name
  location: location
  tags: tags
  sku: {
    name: 'Premium'
    tier: 'Premium'
    capacity: capacity
  }
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${cmkIdentityId}': {}
    }
  }
  properties: {
    premiumMessagingPartitions: 1
    disableLocalAuth: true
    publicNetworkAccess: 'Disabled'
    encryption: {
      keySource: 'Microsoft.KeyVault'
      requireInfrastructureEncryption: true
      keyVaultProperties: [
        {
          keyName: cmkKeyName
          keyVaultUri: keyVaultUri
          identity: {
            userAssignedIdentity: cmkIdentityId
          }
        }
      ]
    }
  }
}

resource queue 'Microsoft.ServiceBus/namespaces/queues@2022-10-01-preview' = [for queueName in queues: {
  parent: namespace
  name: queueName
}]

resource indexerQueue 'Microsoft.ServiceBus/namespaces/queues@2022-10-01-preview' existing = {
  parent: namespace
  name: 'insight-indexer-queue'
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
          privateLinkServiceId: namespace.id
          groupIds: [
            'namespace'
          ]
        }
      }
    ]
  }

  resource dns 'privateDnsZoneGroups@2024-05-01' = {
    name: 'servicebus-dns-zone-group'
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

resource senderRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(namespace.id, workloadPrincipalId, serviceBusDataSenderRoleId)
  scope: namespace
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', serviceBusDataSenderRoleId)
    principalId: workloadPrincipalId
    principalType: 'ServicePrincipal'
  }
}

resource receiverRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(namespace.id, workloadPrincipalId, serviceBusDataReceiverRoleId)
  scope: namespace
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', serviceBusDataReceiverRoleId)
    principalId: workloadPrincipalId
    principalType: 'ServicePrincipal'
  }
}

@description('Resource ID of the Service Bus namespace.')
output id string = namespace.id

@description('Name of the Service Bus namespace.')
output name string = namespace.name

@description('Resource ID of the insight-indexer-queue queue.')
output indexerQueueId string = indexerQueue.id
