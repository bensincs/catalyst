// Cortex — Azure Lighthouse onboarding (the ONE thing a customer deploys).
//
// Deployed once at SUBSCRIPTION scope, this delegates the subscription to the
// Cortex control-plane service principal in the managing (platform) tenant. After
// it lands, the control plane provisions EVERYTHING itself, cross-tenant:
//   • the reconciler container app + its managed identity,
//   • the Microsoft Foundry account / project / model deployments,
//   • the AKS cluster, and
//   • each deployment's application infrastructure.
//
// The customer never runs the footprint template — delegation is the whole
// install. Publish this as a Marketplace *managed service* offer, or run it with:
//
//   az deployment sub create \
//     --subscription <YOUR_SUBSCRIPTION> --location <region> \
//     --template-file onboarding/lighthouse-delegation.bicep
//
// The two platform values below are published by Cortex and prefilled.
//
// Grants (Lighthouse allows only built-in roles):
//   • Contributor — create/manage all the resources above.
//   • User Access Administrator (LIMITED) — the one supported RBAC case: assign
//     the delegatedRoleDefinitionIds below to MANAGED IDENTITIES in your tenant.
//     That's how the control plane grants the reconciler identity Foundry + AKS
//     access. It can grant nothing else, to nobody else.

targetScope = 'subscription'

@description('Cortex (managing) Entra tenant id. Published by Cortex.')
param controlPlaneTenantId string

@description('Object id of the Cortex control-plane service principal in the Cortex tenant. Published by Cortex.')
param controlPlanePrincipalId string

@description('Display name shown to you under Service providers → Delegations.')
param offerName string = 'Cortex managed platform'

// Built-in role definition ids.
var contributorRoleId = 'b24988ac-6180-42a0-ab88-20f7382dd24c'
var userAccessAdminRoleId = '18d7d88d-d35e-4fb5-a5c3-7773c20a72d9'

// The only roles the control plane may assign — and only to managed identities:
//   Foundry User            → the reconciler's Foundry agents/memory data plane
//   AKS RBAC Cluster Admin  → install Argo CD + apply Application CRs
//   AKS Cluster User        → list the cluster's AAD kubeconfig
var foundryUserRoleId = '53ca6127-db72-4b80-b1b0-d745d6d5456d'
var aksRbacClusterAdminRoleId = 'b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b'
var aksClusterUserRoleId = '4abbcc35-e782-43d8-92c5-2d3f1bd2253f'

resource registration 'Microsoft.ManagedServices/registrationDefinitions@2022-10-01' = {
  name: guid(offerName, controlPlaneTenantId, controlPlanePrincipalId)
  properties: {
    registrationDefinitionName: offerName
    description: 'Lets the Cortex control plane deploy + manage the Cortex platform in this subscription.'
    managedByTenantId: controlPlaneTenantId
    authorizations: [
      {
        principalId: controlPlanePrincipalId
        principalIdDisplayName: 'Cortex control plane'
        roleDefinitionId: contributorRoleId
      }
      {
        principalId: controlPlanePrincipalId
        principalIdDisplayName: 'Cortex control plane (role assignment to managed identities)'
        roleDefinitionId: userAccessAdminRoleId
        delegatedRoleDefinitionIds: [
          foundryUserRoleId
          aksRbacClusterAdminRoleId
          aksClusterUserRoleId
        ]
      }
    ]
  }
}

resource assignment 'Microsoft.ManagedServices/registrationAssignments@2022-10-01' = {
  name: guid(registration.id, subscription().id)
  properties: {
    registrationDefinitionId: registration.id
  }
}

output registrationDefinitionId string = registration.id
