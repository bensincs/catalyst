@description('Name of the Key Vault.')
param name string

@description('Azure region for the Key Vault.')
param location string

@description('Tags to apply to all resources in this module.')
param tags object = {}

@description('Azure AD tenant ID for the Key Vault.')
param tenantId string = subscription().tenantId

@description('SKU name for the Key Vault.')
param skuName string = 'premium'

@description('Resource ID of the subnet used for the private endpoint.')
param peSubnetId string

@description('Resource ID of the privatelink.vaultcore.azure.net private DNS zone.')
param dnsZoneId string

@description('Principal ID of the shared workload user-assigned identity to grant secret access.')
param workloadPrincipalId string

var privateEndpointName = 'pe-${name}'
var privateLinkConnectionName = 'psc-${name}'

@description('Built-in "Key Vault Secrets User" role definition ID.')
var keyVaultSecretsUserRoleId = '4633458b-17de-408a-b874-0445c86b69e6'

resource vault 'Microsoft.KeyVault/vaults@2023-07-01' = {
  name: name
  location: location
  tags: tags
  properties: {
    tenantId: tenantId
    sku: {
      family: 'A'
      name: skuName
    }
    enableRbacAuthorization: true
    enableSoftDelete: true
    softDeleteRetentionInDays: 7
    enablePurgeProtection: true
    publicNetworkAccess: 'Disabled'
    networkAcls: {
      defaultAction: 'Deny'
      bypass: 'AzureServices'
    }
  }
}

resource pe 'Microsoft.Network/privateEndpoints@2024-05-01' = {
  name: privateEndpointName
  location: location
  tags: tags
  properties: {
    subnet: {
      id: peSubnetId
    }
    privateLinkServiceConnections: [
      {
        name: privateLinkConnectionName
        properties: {
          privateLinkServiceId: vault.id
          groupIds: [
            'vault'
          ]
        }
      }
    ]
  }

  resource dns 'privateDnsZoneGroups@2024-05-01' = {
    name: 'kv-dns-zone-group'
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
  name: guid(vault.id, workloadPrincipalId, keyVaultSecretsUserRoleId)
  scope: vault
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', keyVaultSecretsUserRoleId)
    principalId: workloadPrincipalId
    principalType: 'ServicePrincipal'
  }
}

@description('Resource ID of the Key Vault.')
output id string = vault.id

@description('Name of the Key Vault.')
output name string = vault.name

@description('URI of the Key Vault.')
output uri string = vault.properties.vaultUri
