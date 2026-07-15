// Cortex control plane — Azure Container Apps IaC.
//
// Deploys the multi-tenant control plane: the Go control-plane API and the
// Next.js console (BFF), each fronted by a custom domain, backed by Azure
// Database for PostgreSQL Flexible Server. Mirrors reconciler/infra/main.bicep
// (Log Analytics + managed environment + container apps, a user-assigned
// identity used to pull images from the registry).
//
//   console  ->  https://catalyst.msft.ae        (external ingress)
//   api      ->  https://api.catalyst.msft.ae    (external ingress; in-tenant reconcilers call this)
//   postgres ->  Azure Database for PostgreSQL Flexible Server (database: cortex)
//
// Images must exist in the registry before the apps can start, so this template
// deploys in two passes gated by `deployApps` — see DEPLOYMENT.md:
//
//   pass 1   deployApps=false   base infra + registry (push images between passes)
//   pass 2   deployApps=true    the two apps, on their default *.azurecontainerapps.io FQDNs
//
// Custom domains + managed certs are then bound out-of-band with the CLI
// (az containerapp hostname add/bind): Container Apps requires a hostname to be
// *added* before its managed cert can be created, which a single template pass
// can't express. After binding, update apps with `az containerapp update`, not by
// re-running this template (a full app PUT would drop the bound domains).

targetScope = 'resourceGroup'

// ─────────────────────────── parameters ───────────────────────────

@description('Azure region for all control-plane resources.')
param location string = resourceGroup().location

@description('Name prefix for resources.')
param namePrefix string = 'cortex'

@description('Public hostname for the console (BFF).')
param consoleDomain string = 'catalyst.msft.ae'

@description('Public hostname for the control-plane API (reconcilers call this).')
param apiDomain string = 'api.catalyst.msft.ae'

@description('Create the container apps. False for the first (registry-only) pass; push images; then true.')
param deployApps bool = false

@description('API image. Defaults to <acr-login-server>/cortex-api:<imageTag>.')
param apiImage string = ''

@description('Console image. Defaults to <acr-login-server>/cortex-console:<imageTag>.')
param consoleImage string = ''

@description('Image tag used when apiImage/consoleImage are not supplied.')
param imageTag string = 'latest'

@description('Entra application (client) ID of the Cortex app registration.')
param entraClientId string

@description('Home tenant ID of the platform — users from this tenant resolve as Platform Admins.')
param platformTenantId string

@description('Entra issuer for console sign-in. Multi-tenant keeps /common.')
param entraIssuer string = 'https://login.microsoftonline.com/common/v2.0'

@description('Value surfaced as NEXT_PUBLIC_CORTEX_ENV (drives the console env badge). One of dev|qa|uat|prod.')
param cortexEnv string = 'prod'

@description('Enable cross-tenant provisioning (Azure Lighthouse): the control-plane identity discovers delegated subscriptions and provisions each tenant footprint + infra.')
param crossTenantProvisioning bool = false

@description('Reconciler image the control plane deploys into each tenant footprint.')
param reconcilerImage string = 'ghcr.io/inception42/cortex-reconciler:latest'

@description('Resource group the control plane deploys each tenant footprint (reconciler + Foundry + AKS) into.')
param footprintResourceGroup string = 'cortex'

@description('Resource group the control plane deploys application infrastructure into.')
param infraResourceGroup string = 'cortex-infra'

@description('PostgreSQL administrator login.')
param postgresAdminUser string = 'cortexadmin'

@description('PostgreSQL administrator password.')
@secure()
param postgresAdminPassword string

@description('Auth.js session secret (e.g. `openssl rand -base64 33`).')
@secure()
param authSecret string

@description('Entra client secret used by the console OAuth flow.')
@secure()
param entraClientSecret string

@description('PostgreSQL Flexible Server compute SKU name.')
param postgresSkuName string = 'Standard_B1ms'

@description('PostgreSQL Flexible Server compute tier.')
@allowed([ 'Burstable', 'GeneralPurpose', 'MemoryOptimized' ])
param postgresSkuTier string = 'Burstable'

@description('PostgreSQL storage size, GiB.')
param postgresStorageGb int = 32

@description('Optional operator IP allowed through the PostgreSQL firewall (for psql/inspection). Empty to skip.')
param operatorIp string = ''

// ─────────────────────────── names ───────────────────────────

var suffix = substring(uniqueString(resourceGroup().id), 0, 8)
var acrName = toLower('${namePrefix}cpacr${suffix}')
var pgName = toLower('${namePrefix}-cp-pg-${suffix}')
var dbName = 'cortex'
var apiAppName = '${namePrefix}-cp-api'
var consoleAppName = '${namePrefix}-cp-console'

var effectiveApiImage = empty(apiImage) ? '${acr.properties.loginServer}/cortex-api:${imageTag}' : apiImage
var effectiveConsoleImage = empty(consoleImage) ? '${acr.properties.loginServer}/cortex-console:${imageTag}' : consoleImage

// Assembled into a container-app secret; never emitted as an output.
var databaseUrl = 'postgres://${postgresAdminUser}:${postgresAdminPassword}@${pg.properties.fullyQualifiedDomainName}:5432/${dbName}?sslmode=require'

// Built-in AcrPull role.
var acrPullRoleId = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '7f951dda-4ed3-4680-a7ca-43fe172d538d')

// ─────────────────────────── base infra (all passes) ───────────────────────────

resource logs 'Microsoft.OperationalInsights/workspaces@2023-09-01' = {
  name: '${namePrefix}-cp-logs'
  location: location
  properties: {
    sku: { name: 'PerGB2018' }
    retentionInDays: 30
  }
}

resource uami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${namePrefix}-cp'
  location: location
}

resource acr 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' = {
  name: acrName
  location: location
  sku: { name: 'Standard' }
  properties: {
    adminUserEnabled: false
  }
}

// Let the apps pull images using the user-assigned identity (no registry admin creds).
resource acrPull 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(acr.id, uami.id, 'AcrPull')
  scope: acr
  properties: {
    principalId: uami.properties.principalId
    roleDefinitionId: acrPullRoleId
    principalType: 'ServicePrincipal'
  }
}

resource pg 'Microsoft.DBforPostgreSQL/flexibleServers@2024-08-01' = {
  name: pgName
  location: location
  sku: {
    name: postgresSkuName
    tier: postgresSkuTier
  }
  properties: {
    version: '16'
    administratorLogin: postgresAdminUser
    administratorLoginPassword: postgresAdminPassword
    storage: { storageSizeGB: postgresStorageGb }
    backup: {
      backupRetentionDays: 7
      geoRedundantBackup: 'Disabled'
    }
    highAvailability: { mode: 'Disabled' }
    authConfig: {
      activeDirectoryAuth: 'Disabled'
      passwordAuth: 'Enabled'
    }
    network: { publicNetworkAccess: 'Enabled' }
  }
}

resource pgDb 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2024-08-01' = {
  parent: pg
  name: dbName
  properties: {
    charset: 'UTF8'
    collation: 'en_US.utf8'
  }
}

// Container Apps egress is Azure-internal; this rule (0.0.0.0) allows Azure services.
resource pgAllowAzure 'Microsoft.DBforPostgreSQL/flexibleServers/firewallRules@2024-08-01' = {
  parent: pg
  name: 'AllowAzureServices'
  properties: {
    startIpAddress: '0.0.0.0'
    endIpAddress: '0.0.0.0'
  }
}

resource pgAllowOperator 'Microsoft.DBforPostgreSQL/flexibleServers/firewallRules@2024-08-01' = if (!empty(operatorIp)) {
  parent: pg
  name: 'OperatorIp'
  properties: {
    startIpAddress: operatorIp
    endIpAddress: operatorIp
  }
}

resource env 'Microsoft.App/managedEnvironments@2024-03-01' = {
  name: '${namePrefix}-cp-env'
  location: location
  properties: {
    appLogsConfiguration: {
      destination: 'log-analytics'
      logAnalyticsConfiguration: {
        customerId: logs.properties.customerId
        sharedKey: logs.listKeys().primarySharedKey
      }
    }
  }
}

// ─────────────────────────── control-plane API (pass 2+) ───────────────────────────

resource api 'Microsoft.App/containerApps@2024-03-01' = if (deployApps) {
  name: apiAppName
  location: location
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${uami.id}': {}
    }
  }
  properties: {
    managedEnvironmentId: env.id
    configuration: {
      activeRevisionsMode: 'Single'
      ingress: {
        external: true
        targetPort: 8080
        transport: 'auto'
        allowInsecure: false
        // api.catalyst.msft.ae is bound out-of-band via the CLI (see DEPLOYMENT.md).
      }
      registries: [
        {
          server: acr.properties.loginServer
          identity: uami.id
        }
      ]
      secrets: [
        {
          name: 'database-url'
          value: databaseUrl
        }
      ]
    }
    template: {
      containers: [
        {
          name: 'api'
          image: effectiveApiImage
          resources: {
            cpu: json('0.5')
            memory: '1Gi'
          }
          env: [
            { name: 'PORT', value: '8080' }
            { name: 'DATABASE_URL', secretRef: 'database-url' }
            { name: 'ENTRA_CLIENT_ID', value: entraClientId }
            { name: 'ENTRA_API_AUDIENCE', value: 'api://${entraClientId}' }
            { name: 'PLATFORM_TENANT_ID', value: platformTenantId }
            { name: 'CORS_ORIGIN', value: 'https://${consoleDomain}' }
            { name: 'SEED_DEMO', value: 'false' }
            // Cross-tenant provisioning (Azure Lighthouse). AZURE_CLIENT_ID selects
            // this user-assigned identity for DefaultAzureCredential — the control
            // plane acts as its own managed identity, no secret held.
            { name: 'AZURE_CLIENT_ID', value: uami.properties.clientId }
            { name: 'CROSS_TENANT_PROVISIONING', value: string(crossTenantProvisioning) }
            { name: 'FOOTPRINT_RESOURCE_GROUP', value: footprintResourceGroup }
            { name: 'INFRA_RESOURCE_GROUP', value: infraResourceGroup }
            { name: 'INFRA_REGION', value: location }
            { name: 'CONTROL_PLANE_PUBLIC_URL', value: 'https://${apiDomain}' }
            { name: 'CORTEX_API_SCOPE', value: 'api://${entraClientId}' }
            { name: 'RECONCILER_IMAGE', value: reconcilerImage }
          ]
        }
      ]
      scale: {
        minReplicas: 1
        maxReplicas: 3
      }
    }
  }
  dependsOn: [
    acrPull
    pgDb
  ]
}

// ─────────────────────────── console / BFF (pass 2+) ───────────────────────────

resource console 'Microsoft.App/containerApps@2024-03-01' = if (deployApps) {
  name: consoleAppName
  location: location
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${uami.id}': {}
    }
  }
  properties: {
    managedEnvironmentId: env.id
    configuration: {
      activeRevisionsMode: 'Single'
      ingress: {
        external: true
        targetPort: 3000
        transport: 'auto'
        allowInsecure: false
        // catalyst.msft.ae is bound out-of-band via the CLI (see DEPLOYMENT.md).
      }
      registries: [
        {
          server: acr.properties.loginServer
          identity: uami.id
        }
      ]
      secrets: [
        {
          name: 'auth-secret'
          value: authSecret
        }
        {
          name: 'entra-client-secret'
          value: entraClientSecret
        }
      ]
    }
    template: {
      containers: [
        {
          name: 'console'
          image: effectiveConsoleImage
          resources: {
            cpu: json('0.5')
            memory: '1Gi'
          }
          env: [
            { name: 'AUTH_URL', value: 'https://${consoleDomain}' }
            { name: 'AUTH_TRUST_HOST', value: 'true' }
            { name: 'AUTH_SECRET', secretRef: 'auth-secret' }
            { name: 'AUTH_MICROSOFT_ENTRA_ID_ID', value: entraClientId }
            { name: 'AUTH_MICROSOFT_ENTRA_ID_SECRET', secretRef: 'entra-client-secret' }
            { name: 'AUTH_MICROSOFT_ENTRA_ID_ISSUER', value: entraIssuer }
            { name: 'PLATFORM_TENANT_ID', value: platformTenantId }
            { name: 'CORTEX_API_URL', value: 'https://${apiDomain}' }
            // The control-plane identity's object id — what customers delegate to
            // via Lighthouse; shown on the install page for copy-paste.
            { name: 'CORTEX_SP_OBJECT_ID', value: uami.properties.principalId }
            { name: 'NEXT_PUBLIC_CORTEX_ENV', value: cortexEnv }
            { name: 'PORT', value: '3000' }
          ]
        }
      ]
      scale: {
        minReplicas: 1
        maxReplicas: 3
      }
    }
  }
  dependsOn: [
    acrPull
  ]
}

// ─────────────────────────── outputs ───────────────────────────
// Computed from base resources so they resolve on every pass (even before the
// apps exist). The app FQDN is deterministic: <app-name>.<env default domain>.

output acrLoginServer string = acr.properties.loginServer
output acrName string = acr.name
output postgresFqdn string = pg.properties.fullyQualifiedDomainName
output envDefaultDomain string = env.properties.defaultDomain
output consoleDefaultFqdn string = '${consoleAppName}.${env.properties.defaultDomain}'
output apiDefaultFqdn string = '${apiAppName}.${env.properties.defaultDomain}'
output consoleDomain string = consoleDomain
output apiDomain string = apiDomain
output uamiClientId string = uami.properties.clientId
// The control-plane identity's object id — the principal customers delegate to via
// Azure Lighthouse (controlPlanePrincipalId / CORTEX_SP_OBJECT_ID).
output uamiPrincipalId string = uami.properties.principalId
