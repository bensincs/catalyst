// Azure Database for PostgreSQL Flexible Server for the todo app.
//
// The admin password is NOT a parameter of this module. It is read from the
// tenant's own Key Vault at deploy time, by ARM, using a Key Vault reference.
//
// That indirection is the whole point. Everything an author passes to a module
// is compiled into the ARM template as a literal, stored against the tenant, and
// kept in that tenant's Azure deployment history permanently — readable by
// anyone with reader access on the resource group. A password has no business
// there. `kv.getSecret()` compiles to a REFERENCE instead: the template carries
// the vault id and the secret's name, and ARM resolves the value itself during
// the deployment. Nothing that handles this template ever sees the credential —
// not the control plane, which cannot read the tenant's vault at all, and not
// the deployment history, which records secure parameters as redacted.
//
// The value is the one the tenant supplied to the `todo database` secret store,
// so the same credential provisions the server and is delivered to the app: one
// secret, entered once, in the tenant's own vault.

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

@description('The tenant\'s own Key Vault, holding the credential. Pass {{vaultName}} and the control plane fills in the tenant\'s vault.')
param vaultName string

@description('Resource group holding that vault. Pass {{vaultResourceGroup}}. The vault lives in the tenant\'s footprint resource group, which is NOT the one this deploys into.')
param vaultResourceGroup string

@description('Name of the vault secret holding the admin password. This is the name a secret store materialises as: set-<secret store id>--<key>.')
param passwordSecretName string = 'set-todo-database--password'

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
param postgresVersion string = '16'

@description('Storage size in GB.')
param storageSizeGB int = 32

@description('Name of the application database created on the server.')
param databaseName string = 'todos'

@description('Allow other Azure services to reach the server.')
param allowAzureServices bool = true

// Existing, because the vault belongs to the tenant and is created by its
// footprint — this module reads from it and never writes to it.
//
// The scope is explicit and load-bearing. An application's infrastructure
// deploys into its own resource group, while the vault sits in the tenant's
// footprint one; a scope-less `existing` resolves in the DEPLOYING group, so
// ARM looks for the vault in the wrong place and fails the deployment with
// KeyVaultParameterReferenceNotFound — which reads as a missing vault rather
// than a misaddressed one.
resource vault 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: vaultName
  scope: resourceGroup(vaultResourceGroup)
}

module server 'server.bicep' = {
  name: '${deployment().name}-pg'
  params: {
    name: name
    location: location
    tags: tags
    administratorLogin: administratorLogin
    // Resolved by ARM from the vault. Never present in this template.
    administratorLoginPassword: vault.getSecret(passwordSecretName)
    skuName: skuName
    skuTier: skuTier
    postgresVersion: postgresVersion
    storageSizeGB: storageSizeGB
    databaseName: databaseName
    allowAzureServices: allowAzureServices
  }
}

// Everything the todo app's chart needs, and nothing it does not. The password
// is deliberately absent: a secure output is not secure once re-exported — it
// lands in cleartext in the deployment's outputs and from there in the app's
// Helm values. The app receives it as a Kubernetes Secret instead.
output host string = server.outputs.host
output port int = server.outputs.port
output databaseName string = server.outputs.databaseName
output administratorLogin string = server.outputs.administratorLogin
output sslMode string = server.outputs.sslMode
output serverId string = server.outputs.serverId
output serverName string = server.outputs.serverName
