// Cortex control plane — Azure Container Apps IaC.
//
// Deploys the multi-tenant control plane: the Go control-plane API and the
// Next.js console (BFF), each fronted by a custom domain, backed by Azure
// Database for PostgreSQL Flexible Server. Mirrors onboarding/footprint.bicep
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

@description('The platform\'s OWN subscription id, where platform-hosted tenants (same subscription, a dedicated RG per tenant) are provisioned. Empty ⇒ platform-hosted tenants disabled. When set, the control-plane identity is granted Contributor + User Access Administrator on it.')
param platformSubscriptionId string = ''

@description('Comma-separated allowlist of platform admins — each entry an email or an Entra object id (oid). When set, only these platform-directory principals are Platform Admins (so ordinary users can live in the platform directory, assigned to tenants). Empty ⇒ any platform-directory user is an admin (back-compat).')
param platformAdminEmails string = ''

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

@description('ACME directory the tenant reconcilers obtain their wildcard certificate from. Empty ⇒ Let\'s Encrypt production.')
param acmeDirectoryUrl string = ''

@description('Contact address registered with the ACME account (expiry notices).')
param acmeEmail string = ''

@description('Managed-certificate resource id for the API custom domain. Empty ⇒ the domain is not bound by this template. A deployment replaces a container app\'s ingress wholesale, so a domain bound out-of-band is DELETED by an apply unless it is declared here.')
param apiCertificateId string = ''

@description('Managed-certificate resource id for the console custom domain. Empty ⇒ not bound.')
param consoleCertificateId string = ''

@description('Repository prefixes a tenant\'s scoped token may pull from the platform registry. Must cover every prefix an upstream is cached into — a chart a tenant can read whose images it cannot is a deploy that ImagePullBackOffs.')
param platformAcrRepos string = 'charts/*,bicep/*,images/*'

@description('Registry-scoped token the CONTROL PLANE uses to inspect cached charts and Bicep modules. Not an Entra identity, because Helm and the Bicep OCI client take a username/password.')
param platformAcrPullUser string = ''

@description('Password for that token.')
@secure()
param platformAcrPullPassword string = ''

@description('Upstream registries cached into the platform registry, as [{name, source, target}] — e.g. { name: \'ghcr-charts\', source: \'ghcr.io/acme/charts/*\', target: \'charts/*\' }. Authors reference the platform registry for these; public registries are still referenced directly.')
param registryCacheRules array = []

@description('Username for the cached upstream (a GitHub username for a GHCR PAT). Empty ⇒ the upstream is public and needs no credential.')
param registryUpstreamUsername string = ''

@description('Password/PAT for the cached upstream. Held in Key Vault and read only by the registry — it is never handed to a tenant, which pulls from the platform registry instead.')
@secure()
param registryUpstreamPassword string = ''

// ─────────────────────────── names ───────────────────────────

var suffix = substring(uniqueString(resourceGroup().id), 0, 8)
var acrName = toLower('${namePrefix}cpacr${suffix}')
var kvName = toLower('${namePrefix}-cp-kv-${suffix}')
var publicAcrName = toLower('${namePrefix}public${suffix}')
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
// Built-in Key Vault Secrets User — what the registry needs to read the
// upstream credential it caches with.
var kvSecretsUserRoleId = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '4633458b-17de-408a-b874-0445c86b69e6')
// Key Vault Secrets Officer — the control plane writes an upstream credential
// when a platform admin adds one from the console.
var kvSecretsOfficerRoleId = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'b86a8fe4-44ce-4948-aee5-eccb2c155cd7')
var cacheUpstreamAuthed = !empty(registryUpstreamPassword)
// A credential set is bound to one upstream login server; take it from the first
// cache rule's source ('ghcr.io/acme/charts/*' → 'ghcr.io').
var registryCacheLoginServer = empty(registryCacheRules) ? '' : first(split(registryCacheRules[0].source, '/'))

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

// Platform-hosted tenants: grant the control-plane identity Contributor + User
// Access Administrator on the platform's own subscription, so it can provision
// per-tenant footprints there (there is no Lighthouse delegation to itself).
module platformSubRoles 'platform-sub-roles.bicep' = if (!empty(platformSubscriptionId)) {
  name: 'cortex-platform-sub-roles'
  scope: subscription(platformSubscriptionId)
  params: {
    controlPlanePrincipalId: uami.properties.principalId
  }
}

resource acr 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' = {
  name: acrName
  location: location
  sku: { name: 'Standard' }
  // Governance disables public network access on untagged resources. Every
  // tenant cluster pulls its charts and images from here over the internet, so
  // losing that would break every deployment in the fleet.
  tags: {
    SecurityControl: 'Ignore'
  }
  // The registry reads the upstream credential from Key Vault itself, so the
  // credential is never passed through a deployment or held by the control plane.
  identity: { type: 'SystemAssigned' }
  properties: {
    adminUserEnabled: false
  }
}

// ── Cached upstreams ────────────────────────────────────────────────────────
// A private chart or Bicep module is mirrored into this registry on first pull
// rather than pulled from its upstream by each tenant. That keeps the upstream
// credential inside the platform: a tenant cluster is given a scoped token for
// this registry only, so it can read the charts it needs and nothing else, and
// revoking one tenant is deleting one token.

resource kv 'Microsoft.KeyVault/vaults@2023-07-01' = {
  name: kvName
  location: location
  // The subscription's governance forces publicNetworkAccess to Disabled and
  // silently reverts attempts to change it — a PATCH answers 200 and the value
  // stays Disabled. This tag is the sanctioned exemption. It is required, not
  // cosmetic: the registry reads the upstream credential over Key Vault's DATA
  // plane, so with access disabled a cache rule falls back to anonymous and
  // every pull of a private upstream fails with 401.
  tags: {
    SecurityControl: 'Ignore'
  }
  properties: {
    tenantId: subscription().tenantId
    sku: { family: 'A', name: 'standard' }
    enableRbacAuthorization: true
    enableSoftDelete: true
    softDeleteRetentionInDays: 7
    publicNetworkAccess: 'Enabled'
    networkAcls: {
      bypass: 'AzureServices'
      defaultAction: 'Allow'
    }
  }
}

resource kvUser 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = if (cacheUpstreamAuthed) {
  parent: kv
  name: 'registry-upstream-username'
  properties: { value: registryUpstreamUsername }
}

resource kvPass 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = if (cacheUpstreamAuthed) {
  parent: kv
  name: 'registry-upstream-password'
  properties: { value: registryUpstreamPassword }
}

resource acrKvRead 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(kv.id, acr.id, 'KeyVaultSecretsUser')
  scope: kv
  properties: {
    principalId: acr.identity.principalId
    roleDefinitionId: kvSecretsUserRoleId
    principalType: 'ServicePrincipal'
  }
}

resource cpKvWrite 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(kv.id, uami.id, 'KeyVaultSecretsOfficer')
  scope: kv
  properties: {
    principalId: uami.properties.principalId
    roleDefinitionId: kvSecretsOfficerRoleId
    principalType: 'ServicePrincipal'
  }
}

resource upstreamCreds 'Microsoft.ContainerRegistry/registries/credentialSets@2023-11-01-preview' = if (cacheUpstreamAuthed) {
  parent: acr
  name: 'upstream'
  identity: { type: 'SystemAssigned' }
  properties: {
    authCredentials: [
      {
        name: 'Credential1'
        usernameSecretIdentifier: kvUser.properties.secretUri
        passwordSecretIdentifier: kvPass.properties.secretUri
      }
    ]
    loginServer: registryCacheLoginServer
  }
  dependsOn: [acrKvRead]
}

// One cache rule per upstream repository pattern. Credentials are attached only
// when the upstream needs them; a public upstream caches anonymously.
resource cacheRules 'Microsoft.ContainerRegistry/registries/cacheRules@2023-11-01-preview' = [
  for rule in registryCacheRules: {
    parent: acr
    name: rule.name
    properties: union(
      {
        sourceRepository: rule.source
        targetRepository: rule.target
      },
      cacheUpstreamAuthed ? { credentialSetResourceId: upstreamCreds.id } : {}
    )
  }
]

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

// The reconciler image is pulled by a Container App in the CUSTOMER's
// subscription, which cannot be granted AcrPull on a platform registry — a
// customer-directory identity is not resolvable here (PrincipalNotFound). So it
// is published to a registry that allows anonymous pull. Only the reconciler
// image goes here; everything private lives in the registry above.
resource publicAcr 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' = {
  name: publicAcrName
  location: location
  sku: { name: 'Standard' }
  tags: {
    SecurityControl: 'Ignore'
  }
  properties: {
    adminUserEnabled: false
    anonymousPullEnabled: true
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
        // Declared, not bound out-of-band: a deployment replaces the ingress
        // wholesale, so a domain attached with the CLI is deleted by the next
        // apply. The certificate is issued once (it needs DNS already pointing
        // here) and its id passed in, so an apply reproduces the binding rather
        // than dropping it.
        customDomains: empty(apiCertificateId) ? [] : [
          {
            name: apiDomain
            bindingType: 'SniEnabled'
            certificateId: apiCertificateId
          }
        ]
      }
      registries: [
        {
          server: acr.properties.loginServer
          identity: uami.id
        }
      ]
      secrets: concat(
        [
          {
            name: 'database-url'
            value: databaseUrl
          }
        ],
        // A credential, so it is a secret rather than a plain value — otherwise
        // it is readable from the app's definition by anyone with read on the
        // resource group.
        empty(platformAcrPullPassword)
          ? []
          : [
              {
                name: 'platform-acr-pull-password'
                value: platformAcrPullPassword
              }
            ]
      )
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
          env: concat([
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
            // Platform-hosted tenants: the platform's own subscription + the
            // platform-admin allowlist (so ordinary users may live in the
            // platform directory, assigned to tenants, without being admins).
            { name: 'PLATFORM_SUBSCRIPTION_ID', value: platformSubscriptionId }
            { name: 'PLATFORM_ADMIN_EMAILS', value: platformAdminEmails }
            // The registry authors reference for private charts and modules.
            // The control plane inspects charts through it, and mints a scoped
            // token per tenant so a cluster can pull from it and nothing else.
            { name: 'ACME_DIRECTORY_URL', value: acmeDirectoryUrl }
            { name: 'ACME_EMAIL', value: acmeEmail }
            { name: 'HELM_OCI_REGISTRY', value: acr.properties.loginServer }
            { name: 'PLATFORM_ACR_NAME', value: acr.name }
            { name: 'PLATFORM_ACR_RESOURCE_ID', value: acr.id }
            { name: 'PLATFORM_ACR_REPOS', value: platformAcrRepos }
            { name: 'HELM_OCI_USERNAME', value: platformAcrPullUser }
            { name: 'BICEP_OCI_USERNAME', value: platformAcrPullUser }
            { name: 'PLATFORM_KEYVAULT_URI', value: kv.properties.vaultUri }
            { name: 'PLATFORM_KEYVAULT_NAME', value: kv.name }
            { name: 'PLATFORM_KEYVAULT_RESOURCE_ID', value: kv.id }
          ],
          // Only when configured: an empty secretRef is a deployment error.
          empty(platformAcrPullPassword) ? [] : [
            { name: 'HELM_OCI_PASSWORD', secretRef: 'platform-acr-pull-password' }
            { name: 'BICEP_OCI_PASSWORD', secretRef: 'platform-acr-pull-password' }
          ])
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
        customDomains: empty(consoleCertificateId) ? [] : [
          {
            name: consoleDomain
            bindingType: 'SniEnabled'
            certificateId: consoleCertificateId
          }
        ]
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
output keyVaultName string = kv.name
output publicAcrName string = publicAcr.name
output publicAcrLoginServer string = publicAcr.properties.loginServer
