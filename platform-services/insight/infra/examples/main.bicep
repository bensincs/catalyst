// Example: consume the insight infra module by relative path.
// Compiles locally (`az bicep build examples/main.bicep`). Replace the
// placeholder resource IDs with real values from your platform.
targetScope = 'resourceGroup'

@description('Unique tenant identifier.')
param tenantName string = 'acme'

@description('Environment name.')
param env string = 'dev'

@description('Kubernetes namespace of the tenant workloads.')
param tenantNamespace string = 'tenant-acme'

@description('AKS OIDC issuer URL.')
param aksOidcIssuerUrl string

@description('Shared private-endpoint subnet resource ID.')
param peSubnetId string

@description('Tenant workspace subdomain.')
param workspaceSubdomain string = 'acme'

@description('Tenant routing domain.')
param routingDomain string = 'example.com'

// Private DNS zone resource IDs (from the connectivity subscription).
param kvDnsZoneId string
param blobDnsZoneId string
param cognitiveDnsZoneId string
param appConfigurationDnsZoneId string
param postgresDnsZoneId string
param redisDnsZoneId string
param searchDnsZoneId string
param servicebusDnsZoneId string

// Secrets — the platform generates and supplies these.
@secure()
param postgresAdministratorPassword string
@secure()
param jwtSecretKey string
@secure()
param spicedbPresharedKey string
@secure()
param llmGatewayMasterKey string

module insight '../main.bicep' = {
  name: 'insight'
  params: {
    tenantName: tenantName
    env: env
    tenantNamespace: tenantNamespace
    aksOidcIssuerUrl: aksOidcIssuerUrl
    peSubnetId: peSubnetId
    workspaceSubdomain: workspaceSubdomain
    routingDomain: routingDomain
    kvDnsZoneId: kvDnsZoneId
    blobDnsZoneId: blobDnsZoneId
    cognitiveDnsZoneId: cognitiveDnsZoneId
    appConfigurationDnsZoneId: appConfigurationDnsZoneId
    postgresDnsZoneId: postgresDnsZoneId
    redisDnsZoneId: redisDnsZoneId
    searchDnsZoneId: searchDnsZoneId
    servicebusDnsZoneId: servicebusDnsZoneId
    postgresAdministratorPassword: postgresAdministratorPassword
    jwtSecretKey: jwtSecretKey
    spicedbPresharedKey: spicedbPresharedKey
    llmGatewayMasterKey: llmGatewayMasterKey
  }
}

// The whole appInfra object is what the platform maps onto the chart's
// .Values.appInfra. Secrets are NOT here — they live in the module-created
// Key Vault and reach pods via the chart's ExternalSecrets.
output appInfra object = insight.outputs.appInfra
output workloadPrincipalId string = insight.outputs.principalId
