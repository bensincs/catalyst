@description('Name of the PostgreSQL flexible server.')
param name string

@description('Azure region for the PostgreSQL flexible server.')
param location string

@description('Tags to apply to all resources in this module.')
param tags object = {}

@description('Major PostgreSQL engine version.')
param version string = '16'

@description('Compute SKU name for the flexible server.')
param skuName string = 'Standard_D4ds_v5'

@description('Compute tier. Must match the SKU family — a Standard_B* name is Burstable, a Standard_D*/E* is GeneralPurpose or MemoryOptimized.')
@allowed([
  'Burstable'
  'GeneralPurpose'
  'MemoryOptimized'
])
param skuTier string = 'GeneralPurpose'

@description('Allocated storage in megabytes.')
param storageMb int = 131072

@description('Administrator login name.')
param administratorLogin string = 'pgadmin'

@description('Administrator login password.')
@secure()
param administratorPassword string

@description('Number of days to retain backups.')
param backupRetentionDays int = 14

@description('Whether geo-redundant backup is enabled.')
param geoRedundantBackup bool = true

@description('Names of the databases to create on the server.')
param databases array = [
  'insight'
  'spicedb'
]

@description('Resource ID of the subnet used for the private endpoint.')
param peSubnetId string

@description('Resource ID of the privatelink.postgres.database.azure.com private DNS zone.')
param dnsZoneId string

var privateEndpointName = 'pe-${name}'
var privateLinkConnectionName = 'psc-${name}'

resource server 'Microsoft.DBforPostgreSQL/flexibleServers@2024-08-01' = {
  name: name
  location: location
  tags: tags
  sku: {
    name: skuName
    tier: skuTier
  }
  properties: {
    version: version
    administratorLogin: administratorLogin
    administratorLoginPassword: administratorPassword
    createMode: 'Default'
    storage: {
      storageSizeGB: storageMb / 1024
    }
    backup: {
      backupRetentionDays: backupRetentionDays
      geoRedundantBackup: geoRedundantBackup ? 'Enabled' : 'Disabled'
    }
    network: {
      publicNetworkAccess: 'Disabled'
    }
  }
}

resource azureExtensions 'Microsoft.DBforPostgreSQL/flexibleServers/configurations@2024-08-01' = {
  parent: server
  name: 'azure.extensions'
  properties: {
    value: 'pgcrypto'
    source: 'user-override'
  }
}

@batchSize(1)
resource database 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2024-08-01' = [for dbName in databases: {
  parent: server
  name: dbName
  properties: {
    charset: 'UTF8'
    collation: 'en_US.utf8'
  }
  dependsOn: [
    azureExtensions
  ]
}]

resource pe 'Microsoft.Network/privateEndpoints@2024-05-01' = {
  name: privateEndpointName
  location: location
  tags: tags
  properties: {
    subnet: {
      id: peSubnetId
    }
    privateLinkServiceConnections: [
      {
        name: privateLinkConnectionName
        properties: {
          privateLinkServiceId: server.id
          groupIds: [
            'postgresqlServer'
          ]
        }
      }
    ]
  }

  resource dns 'privateDnsZoneGroups@2024-05-01' = {
    name: 'psql-dns-zone-group'
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

@description('Resource ID of the PostgreSQL flexible server.')
output id string = server.id

@description('Fully qualified domain name of the PostgreSQL flexible server.')
output fqdn string = server.properties.fullyQualifiedDomainName

@description('Name of the PostgreSQL flexible server.')
output name string = server.name
