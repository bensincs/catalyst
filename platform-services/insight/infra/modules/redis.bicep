// Azure Managed Redis.
//
// NOT Azure Cache for Redis (Microsoft.Cache/redis). New instances of that
// service are refused outright — "Azure Cache for Redis is retiring, create
// Azure Managed Redis instead" — for every tier, Premium and Standard alike.
// Note that `az deployment group validate` happily reports success for such a
// template: the refusal only appears on a real create.
//
// The differences that matter to a caller:
//   * two resources, a cluster and a `databases` child, not one
//   * port 10000, not 6380
//   * the private endpoint group id is `redisEnterprise`, not `redisCache`
//   * it resolves through a REGION-SCOPED private DNS zone,
//     privatelink.<region>.redisenterprise.cache.azure.net
//
// Entra authentication is kept, and access keys are switched off, so this stays
// consistent with the rule that a workload never receives a credential.

@description('Name of the Redis cluster.')
param name string

@description('Azure region for the Redis cluster.')
param location string

@description('Tags to apply to all resources in this module.')
param tags object = {}

@description('Managed Redis SKU, e.g. Balanced_B0. This is not an Azure Cache for Redis SKU name — there is no separate family or capacity.')
param skuName string = 'Balanced_B0'

@description('Resource ID of the subnet used for the private endpoint.')
param peSubnetId string

@description('Resource ID of the privatelink.<region>.redisenterprise.cache.azure.net private DNS zone.')
param dnsZoneId string

@description('Principal ID of the shared workload user-assigned identity to grant cache access.')
param workloadPrincipalId string

resource redis 'Microsoft.Cache/redisEnterprise@2024-10-01' = {
  name: name
  location: location
  tags: tags
  sku: {
    name: skuName
  }
  properties: {
    minimumTlsVersion: '1.2'
  }
}

resource db 'Microsoft.Cache/redisEnterprise/databases@2024-10-01' = {
  parent: redis
  name: 'default'
  properties: {
    clientProtocol: 'Encrypted'
    port: 10000
    // EnterpriseCluster presents a single logical endpoint, so an ordinary
    // non-clustered client works against one host name. OSSCluster would
    // require the client to speak Redis Cluster.
    clusteringPolicy: 'EnterpriseCluster'
    evictionPolicy: 'NoEviction'
    // Entra only. Without this the shared keys remain live and the cache is
    // reachable with a credential nobody is supposed to be holding.
    accessKeysAuthentication: 'Disabled'
  }
}

// Data-plane access. The classic module also carried a Redis Cache Contributor
// role assignment, which is a MANAGEMENT-plane role and grants nothing here —
// on Managed Redis the data path is governed entirely by this access policy.
resource accessPolicyAssignment 'Microsoft.Cache/redisEnterprise/databases/accessPolicyAssignments@2024-10-01' = {
  parent: db
  name: 'insight-workload-identity'
  properties: {
    accessPolicyName: 'default'
    user: {
      objectId: workloadPrincipalId
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
          privateLinkServiceId: redis.id
          groupIds: [
            'redisEnterprise'
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

@description('Resource ID of the Redis cluster.')
output id string = redis.id

@description('Host name of the Redis cluster.')
output hostname string = redis.properties.hostName

@description('Port the database listens on. Managed Redis uses 10000, not 6380.')
output port int = db.properties.port

@description('Name of the Redis cluster.')
output name string = redis.name
