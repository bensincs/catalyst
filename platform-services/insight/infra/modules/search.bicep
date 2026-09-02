@description('Name of the Azure AI Search service.')
param name string

@description('Azure region for the search service.')
param location string

@description('Tags to apply to all resources in this module.')
param tags object = {}

@description('SKU name for the search service.')
param sku string = 'standard'

@description('Number of replicas to distribute the search workload across.')
param replicaCount int = 2

@description('Number of partitions for scaling index size and query throughput.')
param partitionCount int = 1

@description('Resource ID of the customer-managed key user-assigned identity used for encryption.')
param cmkIdentityId string

@description('Resource ID of the subnet used for the private endpoint.')
param peSubnetId string

@description('Resource ID of the privatelink.search.windows.net private DNS zone.')
param dnsZoneId string

@description('Principal ID of the shared workload user-assigned identity to grant search data access.')
param workloadPrincipalId string

@description('Built-in "Search Index Data Contributor" role definition ID.')
var searchIndexDataContributorRoleId = '8ebe5a00-799e-43f5-93ac-243d3dce84a7'

resource search 'Microsoft.Search/searchServices@2024-06-01-preview' = {
  name: name
  location: location
  tags: tags
  sku: {
    name: sku
  }
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${cmkIdentityId}': {}
    }
  }
  properties: {
    replicaCount: replicaCount
    partitionCount: partitionCount
    disableLocalAuth: true
    publicNetworkAccess: 'disabled'
    encryptionWithCmk: {
      enforcement: 'Enabled'
    }
  }
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
          privateLinkServiceId: search.id
          groupIds: [
            'searchService'
          ]
        }
      }
    ]
  }

  resource dns 'privateDnsZoneGroups@2024-05-01' = {
    name: 'search-dns-zone-group'
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
  name: guid(search.id, workloadPrincipalId, searchIndexDataContributorRoleId)
  scope: search
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', searchIndexDataContributorRoleId)
    principalId: workloadPrincipalId
    principalType: 'ServicePrincipal'
  }
}

@description('Resource ID of the search service.')
output id string = search.id

@description('Name of the search service.')
output name string = search.name
