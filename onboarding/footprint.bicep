// Cortex — the per-tenant footprint: the in-tenant reconciler + its Microsoft
// Foundry project (self-contained).
//
// The customer never runs this directly. The Cortex control plane compiles it to
// control-plane/internal/infra/footprint.json (go:embed) and deploys it into each
// enabled, delegated tenant's subscription over Azure Lighthouse. It deploys:
//   • a user-assigned managed identity (the reconciler's Entra identity),
//   • a Microsoft Foundry account + project + a model deployment,
//   • a role assignment granting the identity the Foundry agents data plane,
//   • a Container Apps environment (with Log Analytics) + the reconciler.
//
// The reconciler polls the Cortex control plane and converges the tenant's
// entitled agents into the Foundry project, then heartbeats real install state.
//
// Auth is identity-based end to end — no shared key, no enrollment secret:
//   • to the control plane, it presents its managed identity's Entra token for
//     CORTEX_API_SCOPE, and the control plane maps the token's tid to the tenant;
//   • to Foundry, it presents that same identity's token, authorized by the
//     "Foundry User" role assigned below.
//
// Almost everything is inferred: region (resource group), tenantId/subscriptionId
// (deployment context), the Foundry account name (unique per resource group), and
// the project endpoint (derived from that name). The only required input is the
// organization display name; everything else has a sensible default.
//
// Regenerate the embedded ARM template after editing this file:
//   az bicep build --file onboarding/footprint.bicep \
//     --outfile control-plane/internal/infra/footprint.json

targetScope = 'resourceGroup'

@description('Azure region for all resources.')
param location string = resourceGroup().location

@description('Your organization display name (shown in the Cortex fleet).')
param tenantName string

@description('Cortex control-plane API base URL.')
param controlPlaneUrl string = 'https://api.catalyst.msft.ae'

@description('Entra scope/resource for the Cortex control-plane API.')
param cortexApiScope string = 'api://33e1686e-d227-454a-9974-4978c567720b'

@description('Plan tier.')
@allowed([ 'team', 'enterprise', 'sovereign' ])
param plan string = 'enterprise'

@description('Foundry account (AI Services) name — must be globally unique. Defaults to a stable per-resource-group name.')
param foundryAccountName string = 'cortex-ai-${uniqueString(resourceGroup().id)}'

@description('Foundry project name the reconciler converges agents into.')
param foundryProjectName string = 'agents-prod'

@description('Model deployment name agents reference as their model.')
param modelDeploymentName string = 'gpt-4o'

@description('Model to deploy for agents.')
param modelName string = 'gpt-4o'

@description('Model version. Empty lets Azure pick the current default version, avoiding failures from a deprecated pinned version.')
param modelVersion string = ''

@description('Model deployment SKU (throughput type).')
param modelSkuName string = 'GlobalStandard'

@description('Model capacity, in thousands of tokens-per-minute. Foundry recommends >= 30 for agents/tools.')
param modelCapacity int = 30

@description('Embedding model deployment name — required by memory stores to index memories.')
param embeddingDeploymentName string = 'text-embedding-3-small'

@description('Embedding model to deploy for memory stores.')
param embeddingModelName string = 'text-embedding-3-small'

@description('Embedding model version. Empty lets Azure pick the current default version.')
param embeddingModelVersion string = ''

@description('Embedding deployment SKU (throughput type).')
param embeddingSkuName string = 'Standard'

@description('Embedding capacity, in thousands of tokens-per-minute.')
param embeddingCapacity int = 30

@description('Deploy an AKS cluster for the tenant so the reconciler can bootstrap Argo CD and run Helm/GitOps workloads.')
param deployCluster bool = true

@description('AKS cluster name.')
param clusterName string = 'cortex-aks'

@description('Kubernetes version (blank = the AKS default for the region).')
param kubernetesVersion string = ''

@description('AKS system node pool VM size.')
param nodeVmSize string = 'Standard_D2s_v5'

@description('AKS system node count.')
@minValue(1)
param nodeCount int = 2

@description('Argo CD version the reconciler bootstraps into the cluster.')
param argocdVersion string = 'v2.13.2'

@description('Name of the Kubernetes TLS secret (in the cortex-ingress namespace) the Envoy ingress terminates HTTPS from. Empty = HTTP only (JWT auth still enforced).')
param ingressTlsCredentialName string = ''

@description('Reconciler container image (published by Cortex, or your own registry).')
param reconcilerImage string = 'ghcr.io/inception42/cortex-reconciler:latest'

@description('Private registry server for the reconciler image (e.g. myacr.azurecr.io). Empty = a public image pulled without auth. When set, the reconciler identity pulls with its own identity and needs AcrPull on the registry.')
param registryServer string = ''

@description('Reconciler build/release version reported to the control plane (match the image tag).')
param reconcilerVersion string = '0.1.0'

@description('Reconcile + heartbeat interval, in seconds.')
@minValue(10)
@maxValue(300)
param pollIntervalSeconds int = 30

@description('Deploy the in-Azure reconciler container app. Set false to deploy only the Foundry backing (account/project/model + identity + RBAC) and run the reconciler elsewhere — e.g. locally.')
param deployReconcilerApp bool = true

var prefix = 'cortex'
var tenantId = tenant().tenantId
var subscriptionId = subscription().subscriptionId

// The reconciler drives this endpoint's Foundry Agents API (/agents). Derived
// from the account name (its custom subdomain), matching the documented Foundry
// project endpoint format: https://<account>.services.ai.azure.com/api/projects/<project>.
var foundryProjectEndpoint = 'https://${foundryAccountName}.services.ai.azure.com/api/projects/${foundryProjectName}'
var foundryProjectDisplay = '${foundryAccountName}/${foundryProjectName}'

// Foundry User (formerly "Azure AI User") — grants the agents data plane the
// Foundry Agents API authorizes agent CRUD against: the /agents endpoint checks
// Microsoft.CognitiveServices/accounts/AIServices/agents/*, which this role's
// Microsoft.CognitiveServices/* data actions cover. (Cognitive Services OpenAI
// User's OpenAI/assistants/* does NOT cover it — the new Agents API is off the
// OpenAI data-action namespace.)
var foundryUserRoleId = '53ca6127-db72-4b80-b1b0-d745d6d5456d'

resource logs 'Microsoft.OperationalInsights/workspaces@2023-09-01' = if (deployReconcilerApp) {
  name: '${prefix}-recon-logs'
  location: location
  properties: {
    sku: { name: 'PerGB2018' }
    retentionInDays: 30
  }
}

resource reconIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${prefix}-recon'
  location: location
}

// --- Microsoft Foundry: account + project + model -----------------------------

resource foundryAccount 'Microsoft.CognitiveServices/accounts@2026-05-01' = {
  name: foundryAccountName
  location: location
  kind: 'AIServices'
  sku: { name: 'S0' }
  identity: { type: 'SystemAssigned' }
  properties: {
    // customSubDomainName == account name is what makes the derived
    // services.ai.azure.com endpoint above resolve.
    customSubDomainName: foundryAccountName
    // Turns this AI Services account into a Foundry account that hosts projects.
    allowProjectManagement: true
    publicNetworkAccess: 'Enabled'
    // Entra-only: no API keys. The reconciler authenticates with its identity.
    disableLocalAuth: true
  }
}

resource foundryProject 'Microsoft.CognitiveServices/accounts/projects@2026-05-01' = {
  parent: foundryAccount
  name: foundryProjectName
  location: location
  identity: { type: 'SystemAssigned' }
  properties: {
    displayName: foundryProjectName
    description: 'Cortex-managed agents for ${tenantName}'
  }
}

// A model for agents to run on. Deployed on the account; serialized after the
// project to avoid concurrent writes to the same account.
resource modelDeployment 'Microsoft.CognitiveServices/accounts/deployments@2024-10-01' = {
  parent: foundryAccount
  name: modelDeploymentName
  sku: {
    name: modelSkuName
    capacity: modelCapacity
  }
  properties: {
    model: union({
      format: 'OpenAI'
      name: modelName
    }, empty(modelVersion) ? {} : { version: modelVersion })
  }
  dependsOn: [ foundryProject ]
}

// An embedding model for memory stores to index memories on. Serialized after
// the chat model — deployments on one Cognitive Services account can't be
// written concurrently. Without this, POST /memory_stores fails with
// "Embedding model deployment '…' was not found".
resource embeddingDeployment 'Microsoft.CognitiveServices/accounts/deployments@2024-10-01' = {
  parent: foundryAccount
  name: embeddingDeploymentName
  sku: {
    name: embeddingSkuName
    capacity: embeddingCapacity
  }
  properties: {
    model: union({
      format: 'OpenAI'
      name: embeddingModelName
    }, empty(embeddingModelVersion) ? {} : { version: embeddingModelVersion })
  }
  dependsOn: [ modelDeployment ]
}

// Grant the reconciler identity the agents data plane on the Foundry account.
resource foundryRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(foundryAccount.id, reconIdentity.id, foundryUserRoleId)
  scope: foundryAccount
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', foundryUserRoleId)
    principalId: reconIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    // Cross-tenant (Lighthouse) role assignment of a DELEGATED role to a managed
    // identity requires this — the resource id of the identity being granted the
    // role — or ARM rejects it with "Authorization failed".
    delegatedManagedIdentityResourceId: reconIdentity.id
  }
}

// The project's OWN system-assigned identity also needs Foundry User on the
// project — without it the Foundry portal shows "Setup incomplete: this
// project's managed identity needs the Foundry User role on this project", and
// project-scoped operations (agents, connections, capability hosts) can't use
// the project identity. This is distinct from the reconciler identity above.
resource projectFoundryRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(foundryProject.id, foundryUserRoleId, 'project-mi')
  scope: foundryProject
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', foundryUserRoleId)
    principalId: foundryProject.identity.principalId
    principalType: 'ServicePrincipal'
    delegatedManagedIdentityResourceId: foundryProject.id
  }
}

// --- Kubernetes: AKS cluster the reconciler bootstraps Argo CD into ----------
// AAD-integrated + Azure RBAC, local accounts disabled: the reconciler's own
// managed identity authenticates with an Entra token and is authorized by the
// two role assignments below — no cluster admin kubeconfig secret anywhere.
resource aks 'Microsoft.ContainerService/managedClusters@2024-09-01' = if (deployCluster) {
  name: clusterName
  location: location
  identity: { type: 'SystemAssigned' }
  properties: {
    dnsPrefix: clusterName
    kubernetesVersion: empty(kubernetesVersion) ? null : kubernetesVersion
    enableRBAC: true
    disableLocalAccounts: true
    aadProfile: {
      managed: true
      enableAzureRBAC: true
      tenantID: tenantId
    }
    agentPoolProfiles: [
      {
        name: 'system'
        mode: 'System'
        count: nodeCount
        vmSize: nodeVmSize
        osType: 'Linux'
        osSKU: 'AzureLinux'
        type: 'VirtualMachineScaleSets'
      }
    ]
  }
}

// Azure Kubernetes Service RBAC Cluster Admin — data-plane cluster-admin, so the
// reconciler can install Argo CD and apply Application CRs.
var aksRbacClusterAdminRoleId = 'b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b'
// Azure Kubernetes Service Cluster User Role — the ARM action to list the AAD
// (user) kubeconfig the reconciler builds its client from.
var aksClusterUserRoleId = '4abbcc35-e782-43d8-92c5-2d3f1bd2253f'

resource aksAdminAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (deployCluster) {
  name: guid(clusterName, reconIdentity.id, aksRbacClusterAdminRoleId)
  scope: aks
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', aksRbacClusterAdminRoleId)
    principalId: reconIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    delegatedManagedIdentityResourceId: reconIdentity.id
  }
}

resource aksUserAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (deployCluster) {
  name: guid(clusterName, reconIdentity.id, aksClusterUserRoleId)
  scope: aks
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', aksClusterUserRoleId)
    principalId: reconIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    delegatedManagedIdentityResourceId: reconIdentity.id
  }
}

// --- Reconciler runtime -------------------------------------------------------

resource env 'Microsoft.App/managedEnvironments@2024-03-01' = if (deployReconcilerApp) {
  name: '${prefix}-recon-env'
  location: location
  properties: {
    appLogsConfiguration: {
      destination: 'log-analytics'
      logAnalyticsConfiguration: {
        customerId: logs!.properties.customerId
        sharedKey: logs!.listKeys().primarySharedKey
      }
    }
  }
}

resource reconciler 'Microsoft.App/containerApps@2024-03-01' = if (deployReconcilerApp) {
  name: '${prefix}-reconciler'
  location: location
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${reconIdentity.id}': {}
    }
  }
  properties: {
    managedEnvironmentId: env.id
    configuration: {
      activeRevisionsMode: 'Single'
      // Outbound-only worker: no ingress, no secrets — identity-based auth only.
      // A private registry (e.g. ACR) is pulled with the reconciler's own
      // user-assigned identity; empty registryServer keeps the public-image
      // default (anonymous pull). The identity needs AcrPull on the registry.
      registries: empty(registryServer) ? [] : [
        {
          server: registryServer
          identity: reconIdentity.id
        }
      ]
    }
    template: {
      containers: [
        {
          name: 'reconciler'
          image: reconcilerImage
          resources: {
            cpu: json('0.25')
            memory: '0.5Gi'
          }
          env: [
            { name: 'CONTROL_PLANE_URL', value: controlPlaneUrl }
            { name: 'CORTEX_API_SCOPE', value: cortexApiScope }
            // Selects this user-assigned identity when requesting a token.
            { name: 'AZURE_CLIENT_ID', value: reconIdentity.properties.clientId }
            { name: 'TENANT_ID', value: tenantId }
            { name: 'TENANT_NAME', value: tenantName }
            { name: 'AZURE_REGION', value: location }
            { name: 'AZURE_SUBSCRIPTION_ID', value: subscriptionId }
            { name: 'FOUNDRY_PROJECT', value: foundryProjectDisplay }
            { name: 'FOUNDRY_PROJECT_ENDPOINT', value: foundryProjectEndpoint }
            { name: 'RECONCILER_IDENTITY', value: reconIdentity.name }
            { name: 'RECONCILER_VERSION', value: reconcilerVersion }
            { name: 'PLAN', value: plan }
            { name: 'POLL_INTERVAL_SECONDS', value: string(pollIntervalSeconds) }
            // Kubernetes/GitOps: the reconciler bootstraps Argo CD into this AKS
            // cluster and stamps Argo Applications for the tenant's Helm deploys.
            { name: 'CLUSTER_ENABLED', value: string(deployCluster) }
            { name: 'CLUSTER_NAME', value: clusterName }
            { name: 'CLUSTER_RESOURCE_GROUP', value: resourceGroup().name }
            { name: 'ARGOCD_VERSION', value: argocdVersion }
            { name: 'INGRESS_TLS_CREDENTIAL_NAME', value: ingressTlsCredentialName }
          ]
        }
      ]
      scale: {
        minReplicas: 1 // always running to heartbeat
        maxReplicas: 1
      }
    }
  }
  // The container isn't linked to Foundry by symbolic reference (the endpoint is
  // a derived string), so make the ordering + RBAC propagation explicit.
  dependsOn: [ foundryRoleAssignment, modelDeployment, embeddingDeployment ]
}

output reconcilerPrincipalId string = reconIdentity.properties.principalId
output reconcilerClientId string = reconIdentity.properties.clientId
output reconcilerIdentity string = reconIdentity.name
output tenantId string = tenantId
output subscriptionId string = subscriptionId
output foundryAccountName string = foundryAccountName
output foundryProjectName string = foundryProjectName
output foundryProjectEndpoint string = foundryProjectEndpoint
