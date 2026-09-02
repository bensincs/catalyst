using './main.bicep'

// Non-secret wiring — supply real values from your platform / connectivity sub.
param tenantName = 'acme'
param env = 'dev'
param tenantNamespace = 'tenant-acme'
param aksOidcIssuerUrl = 'https://REPLACE.oic.prod-aks.azure.com/REPLACE/'
param peSubnetId = '/subscriptions/REPLACE/resourceGroups/net/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pe'
param workspaceSubdomain = 'acme'
param routingDomain = 'example.com'
param kvDnsZoneId = '/subscriptions/REPLACE/resourceGroups/connectivity/providers/Microsoft.Network/privateDnsZones/privatelink.vaultcore.azure.net'
param blobDnsZoneId = '/subscriptions/REPLACE/resourceGroups/connectivity/providers/Microsoft.Network/privateDnsZones/privatelink.blob.core.windows.net'
param cognitiveDnsZoneId = '/subscriptions/REPLACE/resourceGroups/connectivity/providers/Microsoft.Network/privateDnsZones/privatelink.cognitiveservices.azure.com'
param appConfigurationDnsZoneId = '/subscriptions/REPLACE/resourceGroups/connectivity/providers/Microsoft.Network/privateDnsZones/privatelink.azconfig.io'
param postgresDnsZoneId = '/subscriptions/REPLACE/resourceGroups/connectivity/providers/Microsoft.Network/privateDnsZones/privatelink.postgres.database.azure.com'
param redisDnsZoneId = '/subscriptions/REPLACE/resourceGroups/connectivity/providers/Microsoft.Network/privateDnsZones/privatelink.redis.cache.windows.net'
param searchDnsZoneId = '/subscriptions/REPLACE/resourceGroups/connectivity/providers/Microsoft.Network/privateDnsZones/privatelink.search.windows.net'
param servicebusDnsZoneId = '/subscriptions/REPLACE/resourceGroups/connectivity/providers/Microsoft.Network/privateDnsZones/privatelink.servicebus.windows.net'

// Secrets — pass via environment variables, never commit them.
param postgresAdministratorPassword = readEnvironmentVariable('PG_ADMIN_PASSWORD', '')
param jwtSecretKey = readEnvironmentVariable('JWT_SECRET_KEY', '')
param spicedbPresharedKey = readEnvironmentVariable('SPICEDB_PRESHARED_KEY', '')
param llmGatewayMasterKey = readEnvironmentVariable('LLM_GATEWAY_MASTER_KEY', '')
