// Infrastructure for agentgateway.
//
// agentgateway runs in standalone mode and reads a configuration file. That
// file is a baseline only: with a database configured, everything managed from
// the UI is written to a database overlay instead of back to the file.
//
// That distinction is the reason this exists. Without a database the UI can
// still be used, but its changes live in the pod and vanish with it, and no two
// replicas can agree on what the configuration says. The database makes the
// configuration durable and shared, which is also what allows more than one
// replica to run.
//
// It holds request logs too, which is where the token and cost accounting comes
// from — none of that survives a restart otherwise.
//
// The admin password is NOT a parameter. It is read from the tenant's own Key
// Vault at deploy time by ARM, using a Key Vault reference. Anything an author
// passes to a module is compiled into the template as a literal and kept in the
// tenant's deployment history permanently; a credential has no business there.
// `kv.getSecret()` compiles to a reference instead, so nothing that handles this
// template ever sees the value.

@description('Name of the PostgreSQL Flexible Server.')
@minLength(3)
@maxLength(63)
param name string = 'agentgateway-{{tenantHash}}'

@description('Region. Must match the private endpoint subnet: a private endpoint cannot cross regions.')
param location string = resourceGroup().location

@description('Tags applied to every resource.')
param tags object = {}

@description('Administrator login for the server.')
param administratorLogin string = 'agwadmin'

@description('Tenant Key Vault holding the administrator password.')
param vaultName string

@description('Resource group of that vault.')
param vaultResourceGroup string

@description('Secret set the password was supplied under.')
param secretSetId string = 'agentgateway-secrets'

@description('Compute size. Burstable is adequate: this stores configuration and request logs, not the request path itself.')
param skuName string = 'Standard_B2ms'

@description('Tier matching the SKU. Passed separately because the SKU name does not carry it.')
param skuTier string = 'Burstable'

@description('Storage in MB.')
param storageMb int = 32768

@description('Days of point-in-time backup retained.')
param backupRetentionDays int = 7

@description('Subnet the private endpoint is created in.')
param peSubnetId string

@description('Resource group holding the private DNS zones.')
param dnsZoneResourceGroup string

var postgresDnsZoneId = resourceId(subscription().subscriptionId, dnsZoneResourceGroup, 'Microsoft.Network/privateDnsZones', 'privatelink.postgres.database.azure.com')

resource tenantVault 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: vaultName
  scope: resourceGroup(vaultResourceGroup)
}

module postgres 'modules/postgres.bicep' = {
  name: 'agentgateway-postgres'
  params: {
    name: name
    location: location
    tags: tags
    skuName: skuName
    skuTier: skuTier
    storageMb: storageMb
    backupRetentionDays: backupRetentionDays
    geoRedundantBackup: false
    administratorLogin: administratorLogin
    administratorPassword: tenantVault.getSecret('set-${secretSetId}--postgres-password')
    databases: [ 'agentgateway' ]
    peSubnetId: peSubnetId
    dnsZoneId: postgresDnsZoneId
  }
}

@description('Fully-qualified hostname of the server.')
output host string = postgres.outputs.fqdn

@description('Port the server listens on.')
output port string = '5432'

@description('Administrator login name.')
output administratorLogin string = administratorLogin

@description('Database holding UI-managed configuration and request logs.')
output databaseName string = 'agentgateway'

@description('TLS is required; a client must not be allowed to fall back to plaintext.')
output sslMode string = 'require'
