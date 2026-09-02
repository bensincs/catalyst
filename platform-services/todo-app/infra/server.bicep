metadata name = 'PostgreSQL Flexible Server'
metadata description = 'Provisions an Azure Database for PostgreSQL Flexible Server, a database, and firewall rules. Emits outputs shaped to feed the todo-app Helm chart.'
metadata owner = 'platform'

targetScope = 'resourceGroup'

// --------------------------------------------------------------------------- //
// Parameters
// --------------------------------------------------------------------------- //

@description('Name of the PostgreSQL Flexible Server (3-63 chars, lowercase letters, numbers and hyphens).')
@minLength(3)
@maxLength(63)
param name string

@description('Azure region for all resources.')
param location string = resourceGroup().location

@description('Tags applied to all resources.')
param tags object = {}

@description('Administrator login name.')
param administratorLogin string = 'todoadmin'

@description('Administrator login password. Supplied by the parent module as a Key Vault reference — never as a literal.')
@secure()
@minLength(8)
param administratorLoginPassword string

@description('Compute SKU name, e.g. Standard_B1ms, Standard_D2ds_v5.')
param skuName string = 'Standard_B1ms'

@description('Compute tier.')
@allowed([
  'Burstable'
  'GeneralPurpose'
  'MemoryOptimized'
])
param skuTier string = 'Burstable'

@description('PostgreSQL major version.')
@allowed([
  '13'
  '14'
  '15'
  '16'
  '17'
])
param postgresVersion string = '16'

@description('Provisioned storage size in GB.')
@allowed([
  32
  64
  128
  256
  512
  1024
  2048
  4096
  8192
  16384
  32768
])
param storageSizeGB int = 32

@description('Automatically grow storage when nearly full.')
@allowed([
  'Enabled'
  'Disabled'
])
param storageAutoGrow string = 'Enabled'

@description('Name of the application database to create.')
@minLength(1)
@maxLength(63)
param databaseName string = 'todos'

@description('Backup retention in days.')
@minValue(7)
@maxValue(35)
param backupRetentionDays int = 7

@description('Geo-redundant backups.')
@allowed([
  'Enabled'
  'Disabled'
])
param geoRedundantBackup string = 'Disabled'

@description('High-availability mode.')
@allowed([
  'Disabled'
  'ZoneRedundant'
  'SameZone'
])
param highAvailabilityMode string = 'Disabled'

@description('Primary availability zone. Empty string lets Azure choose.')
@allowed([
  ''
  '1'
  '2'
  '3'
])
param availabilityZone string = ''

@description('Standby availability zone (only used when highAvailabilityMode is ZoneRedundant).')
@allowed([
  ''
  '1'
  '2'
  '3'
])
param standbyAvailabilityZone string = ''

@description('Allow public network access to the server.')
@allowed([
  'Enabled'
  'Disabled'
])
param publicNetworkAccess string = 'Enabled'

@description('Add a firewall rule permitting other Azure services/resources (0.0.0.0).')
param allowAzureServices bool = true

@description('Additional firewall rules to create.')
param firewallRules array = []
// Example:
// [
//   { name: 'office', startIpAddress: '203.0.113.0', endIpAddress: '203.0.113.255' }
// ]

@description('Server parameters (configurations) to override, e.g. [{ name: "max_connections", value: "100" }].')
param serverConfigurations array = []

// --------------------------------------------------------------------------- //
// Variables
// --------------------------------------------------------------------------- //

var highAvailability = highAvailabilityMode == 'Disabled'
  ? {
      mode: 'Disabled'
    }
  : (empty(standbyAvailabilityZone)
      ? {
          mode: highAvailabilityMode
        }
      : {
          mode: highAvailabilityMode
          standbyAvailabilityZone: standbyAvailabilityZone
        })

// --------------------------------------------------------------------------- //
// Resources
// --------------------------------------------------------------------------- //

resource server 'Microsoft.DBforPostgreSQL/flexibleServers@2024-08-01' = {
  name: name
  location: location
  tags: tags
  sku: {
    name: skuName
    tier: skuTier
  }
  properties: {
    version: postgresVersion
    administratorLogin: administratorLogin
    administratorLoginPassword: administratorLoginPassword
    createMode: 'Default'
    availabilityZone: availabilityZone
    storage: {
      storageSizeGB: storageSizeGB
      autoGrow: storageAutoGrow
    }
    backup: {
      backupRetentionDays: backupRetentionDays
      geoRedundantBackup: geoRedundantBackup
    }
    highAvailability: highAvailability
    network: {
      publicNetworkAccess: publicNetworkAccess
    }
  }
}

resource database 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2024-08-01' = {
  parent: server
  name: databaseName
  properties: {
    charset: 'UTF8'
    collation: 'en_US.utf8'
  }
}

// Firewall rules and databases must not be provisioned concurrently on a
// Flexible Server, so serialise them with dependsOn + batchSize(1).
resource allowAzure 'Microsoft.DBforPostgreSQL/flexibleServers/firewallRules@2024-08-01' = if (allowAzureServices) {
  parent: server
  name: 'AllowAllAzureServicesAndResources'
  properties: {
    startIpAddress: '0.0.0.0'
    endIpAddress: '0.0.0.0'
  }
  dependsOn: [
    database
  ]
}

@batchSize(1)
resource extraFirewallRules 'Microsoft.DBforPostgreSQL/flexibleServers/firewallRules@2024-08-01' = [
  for rule in firewallRules: {
    parent: server
    name: rule.name
    properties: {
      startIpAddress: rule.startIpAddress
      endIpAddress: rule.endIpAddress
    }
    dependsOn: [
      database
      allowAzure
    ]
  }
]

@batchSize(1)
resource configurations 'Microsoft.DBforPostgreSQL/flexibleServers/configurations@2024-08-01' = [
  for config in serverConfigurations: {
    parent: server
    name: config.name
    properties: {
      value: config.value
      source: 'user-override'
    }
    dependsOn: [
      database
    ]
  }
]

// --------------------------------------------------------------------------- //
// Outputs — these are designed to be wired straight into the Helm chart values.
// (The password is intentionally NOT emitted; secrets must not be outputs.)
// --------------------------------------------------------------------------- //

@description('Fully qualified domain name of the server -> Helm value database.host')
output host string = server.properties.fullyQualifiedDomainName

@description('PostgreSQL port -> Helm value database.port')
output port int = 5432

@description('Application database name -> Helm value database.name')
output databaseName string = databaseName

@description('Administrator login -> Helm value database.user')
output administratorLogin string = administratorLogin

@description('Required SSL mode -> Helm value database.sslMode')
output sslMode string = 'require'

@description('Resource ID of the server.')
output serverId string = server.id

@description('Name of the server.')
output serverName string = server.name
