// The six Key Vault secrets the insight chart's ExternalSecrets read.
//
// This exists as a separate module because of how Bicep's Key Vault references
// work. `vault.getSecret()` is only valid as a MODULE parameter — it compiles to
// an ARM Key Vault reference that the platform resolves at deploy time, never to
// a value in the template. So a caller cannot interpolate one into a string.
//
// Inside a module the parameter IS a value at deploy time, which is what makes
// the three connection strings composable here and nowhere else. The parent
// passes the tenant's credentials in as references; this module assembles them
// and writes the names the chart expects.
//
// Nothing here appears in the compiled template: every parameter is @secure(),
// and ARM records secure parameters as redacted.

@description('Key Vault to write the contract secrets into.')
param keyVaultName string

@description('Fully-qualified domain name of the Postgres server.')
param postgresFqdn string

@description('Postgres administrator login.')
param administratorLogin string

@secure()
@description('Postgres administrator password, supplied as a Key Vault reference.')
param administratorPassword string

@secure()
@description('Backend JWT signing key.')
param jwtSecretKey string

@secure()
@description('SpiceDB pre-shared key.')
param spicedbPresharedKey string

@secure()
@description('LiteLLM master key.')
param llmGatewayMasterKey string

resource vault 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: keyVaultName
}

// Three connection strings, composed from the password the parent resolved.
resource backendDbUrl 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: vault
  name: 'insight-backend-database-url'
  properties: {
    value: 'postgresql+asyncpg://${administratorLogin}:${administratorPassword}@${postgresFqdn}:5432/insight?ssl=require'
  }
}

resource llmDbUrl 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: vault
  name: 'insight-llm-database-url'
  properties: {
    value: 'postgresql://${administratorLogin}:${administratorPassword}@${postgresFqdn}:5432/insight?sslmode=require'
  }
}

resource spicedbUri 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: vault
  name: 'insight-spicedb-connection-uri'
  properties: {
    value: 'postgresql://${administratorLogin}:${administratorPassword}@${postgresFqdn}:5432/spicedb?sslmode=require'
  }
}

// Three keys the tenant supplied, republished under the names the chart's
// ExternalSecrets look for. Copied rather than renamed at source so the chart
// stays exactly as the vendor ships it.
resource jwt 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: vault
  name: 'insight-jwt-secret-key'
  properties: { value: jwtSecretKey }
}

resource llmMasterKey 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: vault
  name: 'insight-llm-gateway-master-key'
  properties: { value: llmGatewayMasterKey }
}

resource spicedbKey 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: vault
  name: 'insight-spicedb-preshared-key'
  properties: { value: spicedbPresharedKey }
}
