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
    disableLocalAuth: true
    publicNetworkAccess: 'Disabled'
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

resource configKey 'Microsoft.AppConfiguration/configurationStores/keyValues@2024-05-01' = [for item in configKeys: {
  parent: store
  name: format('{0}\${1}', item.key, label)
  properties: {
    value: item.value
  }
}]

resource featureFlag 'Microsoft.AppConfiguration/configurationStores/keyValues@2024-05-01' = [for item in featureFlags: {
  parent: store
  // A feature flag's key genuinely contains a slash (".appconfig.featureflag/<id>"),
  // but ARM reads a slash in a resource name as a parent/child separator and
  // rejects the name for having three segments where two were expected. The
  // slash has to be percent-encoded; App Configuration decodes it back.
  name: format('.appconfig.featureflag%2F{0}\${1}', item.id, label)
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
