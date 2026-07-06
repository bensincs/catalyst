// Cortex — in-tenant reconciler (Azure Container App).
//
// Packaged as an Azure Marketplace Managed Application: the customer installs it
// into their own subscription. It deploys a user-assigned managed identity, a
// Container Apps environment (with Log Analytics), and the reconciler container,
// which polls the Cortex control plane and heartbeats real tenant/install state.
//
// Auth is identity-based: the reconciler presents its user-assigned managed
// identity's Entra token for the Cortex API — no shared key, no enrollment
// secret. Container Apps injects IDENTITY_ENDPOINT/IDENTITY_HEADER; the
// reconciler requests a token for CORTEX_API_SCOPE using AZURE_CLIENT_ID, and
// the control plane maps the token's tenant id (tid) to this tenant.
//
// tenantId, subscriptionId and region are resolved automatically from the
// deployment — the customer only supplies the control-plane URL and org name.

targetScope = 'resourceGroup'

@description('Azure region for the reconciler resources.')
param location string = resourceGroup().location

@description('Cortex control-plane base URL (the SaaS control plane).')
param controlPlaneUrl string

@description('Entra scope/resource for the Cortex control-plane API (e.g. api://<cortex-app-id>).')
param cortexApiScope string

@description('Your organization display name (shown in the Cortex fleet).')
param tenantName string

@description('Plan tier.')
@allowed([ 'team', 'enterprise', 'sovereign' ])
param plan string = 'enterprise'

@description('The Microsoft Foundry project the reconciler converges agents into (e.g. <project>/agents-prod).')
param foundryProject string

@description('Reconciler container image (published by Cortex).')
param reconcilerImage string = 'ghcr.io/inception42/cortex-reconciler:latest'

@description('Reconciler build/release version reported to the control plane (match the image tag).')
param reconcilerVersion string

@description('Reconcile + heartbeat interval, in seconds.')
@minValue(10)
@maxValue(300)
param pollIntervalSeconds int = 30

var prefix = 'cortex'
var tenantId = tenant().tenantId
var subscriptionId = subscription().subscriptionId

resource logs 'Microsoft.OperationalInsights/workspaces@2023-09-01' = {
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

resource env 'Microsoft.App/managedEnvironments@2024-03-01' = {
  name: '${prefix}-recon-env'
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

resource reconciler 'Microsoft.App/containerApps@2024-03-01' = {
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
            { name: 'FOUNDRY_PROJECT', value: foundryProject }
            { name: 'RECONCILER_IDENTITY', value: reconIdentity.name }
            { name: 'RECONCILER_VERSION', value: reconcilerVersion }
            { name: 'PLAN', value: plan }
            { name: 'POLL_INTERVAL_SECONDS', value: string(pollIntervalSeconds) }
          ]
        }
      ]
      scale: {
        minReplicas: 1 // always running to heartbeat
        maxReplicas: 1
      }
    }
  }
}

output reconcilerPrincipalId string = reconIdentity.properties.principalId
output reconcilerClientId string = reconIdentity.properties.clientId
output reconcilerIdentity string = reconIdentity.name
output tenantId string = tenantId
output subscriptionId string = subscriptionId
