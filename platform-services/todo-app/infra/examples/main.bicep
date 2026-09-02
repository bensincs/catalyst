// Example: consume the PostgreSQL module by relative path.
//
// This compiles locally (`az bicep build examples/main.bicep`) and shows the
// outputs a provisioning platform hooks into the todo-app Helm chart.
//
// To consume the PUBLISHED module from GHCR instead, replace the module path
// with the registry reference (see README.md), e.g.:
//
//   module postgres 'br/ghcr:postgres:0.1.0' = { ... }
//
targetScope = 'resourceGroup'

@description('Password for the PostgreSQL administrator.')
@secure()
@minLength(8)
param administratorLoginPassword string

@description('Short environment name used to derive resource names.')
param environmentName string = 'dev'

module postgres '../main.bicep' = {
  name: 'todo-postgres'
  params: {
    name: 'todo-pg-${environmentName}-${uniqueString(resourceGroup().id)}'
    databaseName: 'todos'
    administratorLogin: 'todoadmin'
    administratorLoginPassword: administratorLoginPassword
    skuName: 'Standard_B1ms'
    skuTier: 'Burstable'
    postgresVersion: '16'
    storageSizeGB: 32
    allowAzureServices: true
  }
}

// The following outputs are exactly what the platform maps onto Helm values.
output host string = postgres.outputs.host
output port int = postgres.outputs.port
output databaseName string = postgres.outputs.databaseName
output administratorLogin string = postgres.outputs.administratorLogin
output sslMode string = postgres.outputs.sslMode
