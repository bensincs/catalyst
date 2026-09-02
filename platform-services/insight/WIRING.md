# Wiring: insight Bicep outputs → Helm chart

The insight infra module and the insight Helm chart meet in two places: a
**non-secret config map** (the `appInfra` output) and **six Key Vault secrets**
(read by the chart's ExternalSecrets). This mirrors the Terraform `configmap`
output contract, so the mapping is 1:1.

```
┌─────────────────────────────┐  appInfra (object)   ┌───────────────────────────┐
│ Bicep: insight infra module  │ ───────────────────▶ │ Helm: .Values.appInfra.*  │
│  (tenant resource group)     │                      │                           │
│                              │  6 KV secrets        │ ExternalSecrets ─▶ pods   │
│  Key Vault ──────────────────┼────────────────────▶ │  (workload identity)      │
└─────────────────────────────┘                      └───────────────────────────┘
        ▲ 4 @secure() params (platform-generated)
```

## 1. Non-secret values — `appInfra` output → `.Values.appInfra`

The module output `appInfra` is a flat object whose **keys are exactly the
`.Values.appInfra.*` keys** the chart consumes. Your platform takes
`deployment.outputs.appInfra.value` and merges it into the chart values (or
projects it as the `app-infra-insight` ConfigMap, as the tenant-operator does).

| appInfra key | Source |
| --- | --- |
| `environment` | `env` |
| `tenantName`, `applicationId` | params |
| `keyvaultTenantId` | subscription tenant |
| `workloadIdentityClientId`, `principalId` | shared UAI |
| `keyvaultUrl` | Key Vault |
| `appConfigEndpoint` | App Configuration |
| `postgresHost`, `postgresDatabases` | Postgres (`insight,spicedb`) |
| `redisHost` | Redis |
| `serviceBusNamespace`, `serviceBusFqdn`, `serviceBusQueues`, `indexerResponseQueue` | Service Bus |
| `aiSearchEndpoint` | AI Search |
| `storageAccountName`, `storageBlobEndpoint`, `storageContainers`, `storagePathPrefixes` | Storage |
| `speechEndpoint`, `speechRegion` | Speech |
| `translatorEndpoint`, `translatorRegion` | Translator |
| `documentIntelligenceEndpoint` | Document Intelligence |
| `spiceDbEndpoint` | `spicedb:50051` |
| `acsEndpoint`, `acsFromEmail` | platform ACS params |

List values (`postgresDatabases`, `serviceBusQueues`, `storageContainers`,
`storagePathPrefixes`) are comma-joined strings — the chart splits them.

## 2. Secrets — Key Vault → ExternalSecrets (never outputs)

The platform generates 4 secrets and passes them as `@secure()` module params:
`postgresAdministratorPassword`, `jwtSecretKey`, `spicedbPresharedKey`,
`llmGatewayMasterKey`. The module writes **6 Key Vault secrets** (the 3 DB
connection strings are built from the password + Postgres FQDN):

| Key Vault secret | Chart ExternalSecret `remoteRef` |
| --- | --- |
| `insight-backend-database-url` | backend database URL |
| `insight-llm-database-url` | llm-gateway (Prisma) database URL |
| `insight-spicedb-connection-uri` | spicedb datastore URI |
| `insight-jwt-secret-key` | backend JWT key |
| `insight-llm-gateway-master-key` | LiteLLM master key |
| `insight-spicedb-preshared-key` | spicedb preshared key |

The chart's per-app `SecretStore` authenticates to this Key Vault using the
shared workload identity (`appInfra.workloadIdentityClientId`), so no secret
travels through Helm values.

## 3. Reference flow

```bash
# 1. Provision (platform supplies secrets + wiring)
OUT=$(az deployment group create -g <tenant-rg> \
  -f infra/bicep/insight/examples/main.bicep -p infra/bicep/insight/examples/dev.bicepparam \
  --query properties.outputs -o json)

# 2. Map the whole appInfra object onto the chart
echo "$OUT" | jq '.appInfra.value' > appInfra.json
helm upgrade --install insight oci://ghcr.io/bensincs/charts/insight-admin-portal \
  --version <v> -f values-<env>.yaml \
  --set-json appInfra="$(cat appInfra.json)"
```

The `values-<env>.yaml` is the chart's existing per-env overlay; `appInfra` is
the generated block. Secrets resolve at runtime via ExternalSecrets.
