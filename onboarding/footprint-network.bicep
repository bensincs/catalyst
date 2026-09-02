// Private-endpoint subnet, created in the AKS-managed virtual network.
//
// Scoped to the cluster's node resource group because that is where AKS puts the
// virtual network, and a private endpoint has to sit in a subnet of the same
// network the workloads run in — otherwise the pods cannot resolve or reach it.
//
// Kept as a separate module for the same reason footprint-noderg.bicep is: the
// node resource group is created by AKS, not by the footprint, so anything
// targeting it has to be deployed at that scope and ordered after the cluster.

targetScope = 'resourceGroup'

@description('Name of the AKS-managed virtual network in this resource group.')
param vnetName string

@description('Subnet name for private endpoints.')
param subnetName string = 'snet-private-endpoints'

@description('Address prefix for that subnet. Must be free within the cluster vnet.')
param addressPrefix string

resource vnet 'Microsoft.Network/virtualNetworks@2023-11-01' existing = {
  name: vnetName
}

resource peSubnet 'Microsoft.Network/virtualNetworks/subnets@2023-11-01' = {
  parent: vnet
  name: subnetName
  properties: {
    addressPrefix: addressPrefix
    // Private endpoints need this off. It is the default on newer API versions
    // but is stated because the whole subnet exists for them.
    privateEndpointNetworkPolicies: 'Disabled'
    privateLinkServiceNetworkPolicies: 'Enabled'
  }
}

output subnetId string = peSubnet.id
output vnetId string = vnet.id
