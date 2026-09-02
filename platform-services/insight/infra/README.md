# insight — per-app Azure infrastructure (Bicep)

Full Azure footprint for the **insight** admin-portal app, ported to Bicep from
the Terraform at `cortex-post-mvp/infra/tenant/app/insight`. Deploy it **into the
tenant resource group** (`targetScope = 'resourceGroup'`); the provisioning
platform supplies the wiring inputs and maps the outputs onto the insight Helm
chart.

## What it creates

| Resource | Notes |
| --- | --- |
| Shared workload identity (UAI) + 8 federated credentials | one per chart ServiceAccount (`backend-sa`, `bff-sa`, `appconfig-provider-sa`, `agentic-core-sa`, `llm-gateway-sa`, `digital-simulator-sa`, `decision-register-sa`, `translation-sa`) |
| Key Vault (Premium) + PE | RBAC, purge protection; holds the 6 secrets + 3 CMK keys |
| 3 CMK identities + 3 RSA-HSM keys + role grants | Storage / Search / Service Bus double encryption |
| Postgres Flexible Server + PE | databases `insight` + `spicedb`, `pgcrypto`, SSL, private only |
| Redis (Premium, Entra auth) + PE + Data Owner access policy | |
| Service Bus (Premium, CMK) + PE | queues `meeting-queue`, `insight-indexer-queue` |
| Storage (StorageV2, CMK, AAD-only) + PE | containers `insight-user-documents`, `insight-audio-brief` |
| AI Search (Standard, CMK enforcement) + PE | |
| Cognitive x3 + PE | Speech (UAI + BYOS), Translator, Document Intelligence |
| App Configuration + PE | ~64 config keys + 150 feature flags (label = `env`) |
| 10 data-plane role assignments | shared UAI → App Config / Storage / Search / Service Bus / Redis / Cognitive |
| 6 Key Vault secrets | see below |

> Simplification vs. the Terraform: Key Vault keys/secrets and App Config
> key-values are created as **ARM control-plane** resources, so the TF
> `time_sleep` waits and deployer data-plane role grants are unnecessary.

## Inputs (module parameters)

Wiring (required): `tenantName`, `env`, `tenantNamespace`, `aksOidcIssuerUrl`,
`peSubnetId`, `workspaceSubdomain`, `routingDomain`, and the 8 private DNS zone
IDs (`kvDnsZoneId`, `blobDnsZoneId`, `cognitiveDnsZoneId`,
`appConfigurationDnsZoneId`, `postgresDnsZoneId`, `redisDnsZoneId`,
`searchDnsZoneId`, `servicebusDnsZoneId`).

Secrets (required, `@secure()`, platform-generated): `postgresAdministratorPassword`,
`jwtSecretKey`, `spicedbPresharedKey`, `llmGatewayMasterKey`.

Optional: platform ACS (`platformAcsEndpoint`, `platformAcsFromEmail`, and
`platformAcsName` + `platformAcsResourceGroup` to grant the email-sender role),
plus sizing params (all defaulted to the validated Terraform values).

## Outputs → chart

- `appInfra` (object) — maps 1:1 onto the chart's `.Values.appInfra.*`. See
  [`docs/insight-wiring.md`](../../../docs/insight-wiring.md).
- `principalId`, `storageAccountId`, `indexerResponseQueueId`,
  `indexerResponseQueueName` — for cross-app RBAC.

The 6 secrets are **not** outputs — they are written into the module-created Key
Vault and reach pods via the chart's `ExternalSecrets`:

| Key Vault secret | Contents |
| --- | --- |
| `insight-backend-database-url` | `postgresql+asyncpg://…/insight?ssl=require` |
| `insight-llm-database-url` | `postgresql://…/insight?sslmode=require` |
| `insight-spicedb-connection-uri` | `postgresql://…/spicedb?sslmode=require` |
| `insight-jwt-secret-key` | backend JWT signing key |
| `insight-llm-gateway-master-key` | LiteLLM master key |
| `insight-spicedb-preshared-key` | SpiceDB gRPC pre-shared key |

## Platform prerequisites

The app assumes these exist (module takes them as inputs / the cluster provides
them): a shared PE subnet, the 8 private DNS zones, the AKS OIDC issuer, and
in-cluster platform services the chart depends on (External Secrets Operator,
Gateway API, workload identity, and the AI Gateway / authz-service the app calls).

## Validate / build

```bash
az bicep build --file main.bicep
az bicep build --file examples/main.bicep
```

## Publish to GHCR (OCI artifact, via oras — same as the postgres module)

```bash
az bicep build --file main.bicep --outfile main.json
printf '{}' > config.json
oras push \
  --config config.json:application/vnd.ms.bicep.module.config.v1+json \
  ghcr.io/bensincs/bicep/insight:0.1.0 \
  main.json:application/vnd.ms.bicep.module.layer.v1+json
```
