@description('Name of the Redis cache.')
param name string

@description('Azure region for the Redis cache.')
param location string

@description('Tags to apply to all resources in this module.')
param tags object = {}

@description('Number of cache instances in the selected SKU.')
param capacity int = 1

@description('SKU family for the Redis cache.')
param family string = 'P'

@description('SKU name for the Redis cache.')
param skuName string = 'Premium'

@description('Resource ID of the subnet used for the private endpoint.')
param peSubnetId string

@description('Resource ID of the privatelink.redis.cache.windows.net private DNS zone.')
param dnsZoneId string

@description('Principal ID of the shared workload user-assigned identity to grant cache access.')
param workloadPrincipalId string

@description('Client ID of the shared workload user-assigned identity used as the access policy alias.')
param workloadClientId string

@description('Built-in "Redis Cache Contributor" role definition ID.')
var redisCacheContributorRoleId = 'e0f68234-74aa-48ed-b826-c38b57376e17'

resource redis 'Microsoft.Cache/redis@2024-03-01' = {
  name: name
  location: location
  tags: tags
  properties: {
    sku: {
      name: skuName
      family: family
      capacity: capacity
    }
    minimumTlsVersion: '1.2'
    enableNonSslPort: false
    publicNetworkAccess: 'Disabled'
    redisConfiguration: {
      'aad-enabled': 'true'
    }
  }
}

resource accessPolicyAssignment 'Microsoft.Cache/redis/accessPolicyAssignments@2024-03-01' = {
  parent: redis
  name: 'insight-workload-identity'
  properties: {
    accessPolicyName: 'Data Owner'
    objectId: workloadPrincipalId
    objectIdAlias: workloadClientId
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
          privateLinkServiceId: redis.id
          groupIds: [
            'redisCache'
          ]
        }
      }
    ]
  }

  resource dns 'privateDnsZoneGroups@2024-05-01' = {
    name: 'redis-dns-zone-group'
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
  name: guid(redis.id, workloadPrincipalId, redisCacheContributorRoleId)
  scope: redis
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', redisCacheContributorRoleId)
    principalId: workloadPrincipalId
    principalType: 'ServicePrincipal'
  }
}

@description('Resource ID of the Redis cache.')
output id string = redis.id

@description('Host name of the Redis cache.')
output hostname string = redis.properties.hostName

@description('Name of the Redis cache.')
output name string = redis.name
