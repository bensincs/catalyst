// Infrastructure for the authorization service.
//
// SpiceDB's datastore. It was in-cluster Postgres while the service was being
// extracted from the Cortex platform chart, which is fine for a throwaway
// stamp and wrong for anything else: the authorization store is where every
// access decision is ultimately resolved, so it should not live on a pod's
// ephemeral disk.
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
param name string = 'authz-{{tenantHash}}'

@description('Azure region. Pass {{region}} — a private endpoint must be in the same region as its subnet.')
param location string = resourceGroup().location

@description('Tags applied to all resources.')
param tags object = {}

@description('Administrator login name.')
param administratorLogin string = 'authzadmin'

@description('The tenant\'s own Key Vault, holding the credential. Pass {{vaultName}}.')
param vaultName string

@description('Resource group holding that vault. Pass {{vaultResourceGroup}}.')
param vaultResourceGroup string

@description('Identifier of the secret store supplying the password, used to derive the vault secret name.')
param secretSetId string = 'authz-secrets'

@description('Compute SKU name. A bare name — the tier is separate.')
param skuName string = 'Standard_B2ms'

@description('Compute tier; must match the SKU family.')
@allowed([
  'Burstable'
  'GeneralPurpose'
  'MemoryOptimized'
])
param skuTier string = 'Burstable'

@description('Storage size in MB.')
param storageMb int = 32768

@description('Backup retention in days.')
param backupRetentionDays int = 7

@description('Resource ID of the subnet used for the private endpoint. Pass {{peSubnetId}}.')
param peSubnetId string

@description('Resource group holding the private DNS zones. Pass {{dnsZoneResourceGroup}}.')
param dnsZoneResourceGroup string

var postgresDnsZoneId = resourceId(subscription().subscriptionId, dnsZoneResourceGroup, 'Microsoft.Network/privateDnsZones', 'privatelink.postgres.database.azure.com')

resource tenantVault 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: vaultName
  scope: resourceGroup(vaultResourceGroup)
}

module postgres 'modules/postgres.bicep' = {
  name: 'authz-postgres'
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
    databases: [ 'spicedb' ]
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

@description('Database SpiceDB stores its relationships in.')
output databaseName string = 'spicedb'

@description('TLS is required; a client must not be allowed to fall back to plaintext.')
output sslMode string = 'require'
