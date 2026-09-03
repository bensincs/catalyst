@description('Name of the App Configuration store.')
param name string

@description('Azure region for the App Configuration store.')
param location string

@description('Tags to apply to all resources in this module.')
param tags object = {}

@description('SKU name for the App Configuration store.')
param sku string = 'standard'

@description('Environment label applied to every App Configuration key value.')
param label string

@description('Configuration key values to seed. Each item: { key: string, value: string }.')
param configKeys array = []

@description('Feature flags to seed. Each item: { id: string, description: string, enabled: bool }.')
param featureFlags array = []

@description('Principal ID of the workload identity that reads this configuration at runtime.')
param workloadPrincipalId string

@description('Resource ID of the subnet used for the private endpoint.')
param peSubnetId string

@description('Resource ID of the privatelink.azconfig.io private DNS zone.')
param dnsZoneId string

resource store 'Microsoft.AppConfiguration/configurationStores@2024-05-01' = {
  name: name
  location: location
  tags: tags
  sku: {
    name: sku
  }
  properties: {
    // Written by ARM, not by the workload, and that is what constrains this.
    //
    // With public network access disabled, ARM can only reach the data plane
    // via privateLinkDelegation — and Azure refuses that unless the
    // authentication mode is Pass-through ("Data plane proxy authentication
    // mode must be set to Pass-through to enable private link delegation").
    // Pass-through in that configuration returned Forbidden on every key-value
    // write, and still did twelve minutes after the deploying principal was
    // granted App Configuration Data Owner, so it was not RBAC propagation —
    // ARM was being refused at the network layer.
    //
    // So the store accepts public network connections while keeping local
    // authentication OFF: no access keys exist, ARM writes as the deploying
    // principal via Pass-through, and the workload still reads with its managed
    // identity. Reaching the store therefore always requires an Entra identity
    // holding a data-plane role; what is given up is network-level isolation of
    // the management path, not authentication.
    disableLocalAuth: true
    publicNetworkAccess: 'Enabled'
    dataPlaneProxy: {
      authenticationMode: 'Pass-through'
      privateLinkDelegation: 'Disabled'
    }
  }
}

resource pe 'Microsoft.Network/privateEndpoints@2024-05-01' = {
  name: 'pe-${name}'
  location: location
  tags: tags
  properties: {
    subnet: {
      id: peSubnetId
    }
    privateLinkServiceConnections: [
      {
        name: 'psc-${name}'
        properties: {
          privateLinkServiceId: store.id
          groupIds: [
            'configurationStores'
          ]
        }
      }
    ]
  }

  resource dns 'privateDnsZoneGroups@2024-05-01' = {
    name: 'appcs-dns-zone-group'
    properties: {
      privateDnsZoneConfigs: [
        {
          name: 'config'
          properties: {
            privateDnsZoneId: dnsZoneId
          }
        }
      ]
    }
  }
}

// App Configuration Data Owner, held by whoever runs the deployment: with
// Pass-through, ARM performs the key-value writes below as that identity.
var appConfigurationDataOwnerRoleId = '5ae67dd6-50cb-40e7-96ff-dc2bfa4b606b'

// App Configuration Data Reader for the workload. Without it the store exists,
// is reachable and holds every key, and the application still fails with
// Forbidden on startup — the configuration is written by the deployment but was
// never readable by the thing that consumes it.
var appConfigurationDataReaderRoleId = '516239f1-63e1-4d78-a4de-a74fb236a071'

resource workloadDataAccess 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(store.id, workloadPrincipalId, appConfigurationDataReaderRoleId)
  scope: store
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', appConfigurationDataReaderRoleId)
    principalId: workloadPrincipalId
    principalType: 'ServicePrincipal'
  }
}

resource deployerDataAccess 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(store.id, deployer().objectId, appConfigurationDataOwnerRoleId)
  scope: store
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', appConfigurationDataOwnerRoleId)
    principalId: deployer().objectId
  }
}

resource configKey 'Microsoft.AppConfiguration/configurationStores/keyValues@2024-05-01' = [for item in configKeys: {
  parent: store
  dependsOn: [ deployerDataAccess ]
  name: format('{0}\${1}', item.key, label)
  properties: {
    value: item.value
  }
}]

resource featureFlag 'Microsoft.AppConfiguration/configurationStores/keyValues@2024-05-01' = [for item in featureFlags: {
  parent: store
  // A feature flag's key genuinely contains a slash (".appconfig.featureflag/<id>"),
  // but ARM reads a slash in a resource name as a parent/child separator and
  // rejects the name for having three segments where two were expected.
  //
  // App Configuration escapes it as ~2F, NOT the %2F you would expect: %2F is
  // accepted by the template parser and then rejected by the resource provider
  // with KeyValueNameInvalid. Confirmed by creating both against a real store —
  // `az deployment group validate` reports BOTH as valid, so it cannot be used
  // to check this.
  name: format('.appconfig.featureflag~2F{0}\${1}', item.id, label)
  dependsOn: [ deployerDataAccess ]
  properties: {
    contentType: 'application/vnd.microsoft.appconfig.ff+json;charset=utf-8'
    value: string({
      id: item.id
      description: item.description
      enabled: item.enabled
      conditions: {
        client_filters: []
      }
    })
  }
}]

@description('Resource ID of the App Configuration store.')
output id string = store.id

@description('Name of the App Configuration store.')
output name string = store.name

@description('Endpoint of the App Configuration store.')
output endpoint string = store.properties.endpoint
