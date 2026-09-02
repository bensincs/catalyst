@description('Name of the storage account.')
param name string

@description('Azure region for the storage account.')
param location string

@description('Tags to apply to all resources in this module.')
param tags object = {}

@description('SKU name for the storage account.')
param sku string = 'Standard_ZRS'

@description('Names of the blob containers to create.')
param containers array = [
  'insight-user-documents'
  'insight-audio-brief'
]

@description('Resource ID of the customer-managed key user-assigned identity used for encryption.')
param cmkIdentityId string

@description('Name of the customer-managed key in Key Vault.')
param cmkKeyName string

@description('URI of the Key Vault holding the customer-managed key.')
param keyVaultUri string

@description('Resource ID of the subnet used for the private endpoint.')
param peSubnetId string

@description('Resource ID of the privatelink.blob.core.windows.net private DNS zone.')
param dnsZoneId string

@description('Principal ID of the shared workload user-assigned identity to grant blob access.')
param workloadPrincipalId string

@description('Number of days after modification before blobs are deleted. Set to 0 to disable the lifecycle policy.')
param lifecycleDeleteAfterDays int = 0

@description('Built-in "Storage Blob Data Contributor" role definition ID.')
var storageBlobDataContributorRoleId = 'ba92f5b4-2d11-453d-a403-e96b0029c9fe'

resource storage 'Microsoft.Storage/storageAccounts@2023-05-01' = {
  name: name
  location: location
  tags: tags
  kind: 'StorageV2'
  sku: {
    name: sku
  }
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${cmkIdentityId}': {}
    }
  }
  properties: {
    minimumTlsVersion: 'TLS1_2'
    supportsHttpsTrafficOnly: true
    publicNetworkAccess: 'Disabled'
    allowSharedKeyAccess: false
    defaultToOAuthAuthentication: true
    allowBlobPublicAccess: false
    allowCrossTenantReplication: false
    allowedCopyScope: 'AAD'
    networkAcls: {
      defaultAction: 'Deny'
      bypass: 'AzureServices, Metrics'
    }
    encryption: {
      identity: {
        userAssignedIdentity: cmkIdentityId
      }
      keySource: 'Microsoft.Keyvault'
      requireInfrastructureEncryption: true
      keyvaultproperties: {
        keyname: cmkKeyName
        keyvaulturi: keyVaultUri
      }
      services: {
        blob: {
          enabled: true
          keyType: 'Account'
        }
        file: {
          enabled: true
          keyType: 'Account'
        }
        queue: {
          enabled: true
          keyType: 'Account'
        }
        table: {
          enabled: true
          keyType: 'Account'
        }
      }
    }
  }
}

resource blobServices 'Microsoft.Storage/storageAccounts/blobServices@2023-05-01' = {
  parent: storage
  name: 'default'
  properties: {
    isVersioningEnabled: true
    containerDeleteRetentionPolicy: {
      enabled: true
      days: 7
    }
  }
}

resource container 'Microsoft.Storage/storageAccounts/blobServices/containers@2023-05-01' = [for containerName in containers: {
  parent: blobServices
  name: containerName
  properties: {
    publicAccess: 'None'
  }
}]

resource lifecyclePolicy 'Microsoft.Storage/storageAccounts/managementPolicies@2023-05-01' = if (lifecycleDeleteAfterDays > 0) {
  parent: storage
  name: 'default'
  properties: {
    policy: {
      rules: [
        {
          name: 'tenant-lifecycle'
          enabled: true
          type: 'Lifecycle'
          definition: {
            filters: {
              blobTypes: [
                'blockBlob'
              ]
            }
            actions: {
              baseBlob: {
                delete: {
                  daysAfterModificationGreaterThan: lifecycleDeleteAfterDays
                }
              }
            }
          }
        }
      ]
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
          privateLinkServiceId: storage.id
          groupIds: [
            'blob'
          ]
        }
      }
    ]
  }

  resource dns 'privateDnsZoneGroups@2024-05-01' = {
    name: 'blob-dns-zone-group'
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

resource ra 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(storage.id, workloadPrincipalId, storageBlobDataContributorRoleId)
  scope: storage
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', storageBlobDataContributorRoleId)
    principalId: workloadPrincipalId
    principalType: 'ServicePrincipal'
  }
}

@description('Resource ID of the storage account.')
output id string = storage.id

@description('Name of the storage account.')
output name string = storage.name

@description('Primary blob endpoint of the storage account.')
output primaryBlobEndpoint string = storage.properties.primaryEndpoints.blob
