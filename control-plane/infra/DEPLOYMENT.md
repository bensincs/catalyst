# Cortex control plane — deployment

Infrastructure-as-code for the Cortex control plane on **Azure Container Apps**,
backed by **Azure Database for PostgreSQL Flexible Server**, fronted by custom
domains on `sincs.dev`.

```
 browser ──▶ https://catalyst.sincs.dev        console  (Next.js BFF, external ingress)
                     │ server-side
                     ▼
             https://api.catalyst.sincs.dev     control-plane API (Go, external ingress)
                     │
                     ▼
             cortex-cp-pg-*.postgres.database.azure.com   PostgreSQL (database: cortex)

 in-tenant reconcilers (reconciler/infra) ──▶ https://api.catalyst.sincs.dev  (/recon/*)
```

The API is public because the in-tenant reconcilers (shipped by `reconciler/infra/`)
call it from customer subscriptions. Both services authenticate against the same
Entra app registration; no shared secrets cross the boundary.

Everything is in [`main.bicep`](./main.bicep) with defaults in
[`main.bicepparam`](./main.bicepparam):

| Resource | Name | Notes |
| --- | --- | --- |
| Log Analytics | `cortex-cp-logs` | Container Apps logs |
| User-assigned identity | `cortex-cp` | pulls images from ACR (AcrPull) |
| Container Registry | `cortexcpacr<hash>` | holds the two images |
| PostgreSQL Flexible Server | `cortex-cp-pg-<hash>` | database `cortex`, SSL required |
| Managed environment | `cortex-cp-env` | Container Apps env |
| Container app — API | `cortex-cp-api` | `api.catalyst.sincs.dev`, port 8080 |
| Container app — console | `cortex-cp-console` | `catalyst.sincs.dev`, port 3000 |

---

## Why three passes

Container Apps custom domains have two ordering constraints: an image must exist
in the registry before an app can start, and DNS must resolve before a managed
certificate can be issued. The template handles both with two flags:

| Pass | `deployApps` | `bindCustomDomains` | Creates |
| --- | --- | --- | --- |
| 1 | `false` | `false` | registry, Postgres, env, identity |
| 2 | `true` | `false` | the two apps, on their default `*.azurecontainerapps.io` FQDNs |
| 3 | `true` | `true` | managed certs + binds `catalyst.sincs.dev` / `api.catalyst.sincs.dev` |

---

## Prerequisites

- **Azure CLI** with Bicep: `az upgrade && az bicep upgrade`
- An Azure subscription; rights to create resources **and role assignments**
  (Owner, or Contributor + User Access Administrator)
- **DNS control of `sincs.dev`** (to add `catalyst` records)
- An **Entra app registration** for Cortex (see [Step 1](#1-entra-app-registration))
- No Docker needed locally — images build in the cloud with `az acr build`

Log in and select the subscription:

```bash
az login
az account set --subscription "<SUBSCRIPTION_ID>"
```

---

## 1. Entra app registration

One multi-tenant app backs both the console (delegated sign-in) and the API
(token audience). If you already followed [`../../ENTRA.md`](../../ENTRA.md),
you only need to add the production redirect URI in Step 8.

1. **Register** an app (Microsoft Entra ID → App registrations → New):
   - Supported account types: **Accounts in any organizational directory** (multi-tenant).
   - Note the **Application (client) ID** → this is `CORTEX_ENTRA_CLIENT_ID`.
2. **Expose an API**:
   - Set the Application ID URI to `api://<client-id>`.
   - Add a delegated scope named **`access_as_user`** (admins + users can consent).
   - Pre-authorize the Azure CLI app `04b07795-8ddb-461a-bbee-02f9e1bf7b46`
     (and each in-tenant reconciler's managed identity) for that scope.
3. **API permissions**: Microsoft Graph → delegated `openid`, `profile`, `email`, `User.Read`.
4. **Certificates & secrets** → New client secret → copy the value → `CORTEX_ENTRA_CLIENT_SECRET`.
5. **Redirect URIs** (Web): add both so local dev and prod work —
   - `http://localhost:4200/api/auth/callback/microsoft-entra-id`
   - `https://catalyst.sincs.dev/api/auth/callback/microsoft-entra-id`

---

## 2. Resource group + secrets

```bash
export LOCATION="uaenorth"
export RG="cortex-control-plane"
az group create -n "$RG" -l "$LOCATION"
```

Export the config the parameter file reads from your shell (nothing secret is
written to disk):

```bash
export CORTEX_ENTRA_CLIENT_ID="<client-id>"
export CORTEX_PLATFORM_TENANT_ID="<your platform tenant guid>"   # users from this tenant are Platform Admins

export CORTEX_PG_ADMIN_PASSWORD="$(openssl rand -base64 24)"     # store in your password manager
export CORTEX_AUTH_SECRET="$(openssl rand -base64 33)"           # Auth.js session secret
export CORTEX_ENTRA_CLIENT_SECRET="<client secret from step 1.4>"

# optional: your public IP, to reach Postgres with psql
# export CORTEX_OPERATOR_IP="$(curl -s ifconfig.me)"
```

> Save `CORTEX_PG_ADMIN_PASSWORD` — it is the Postgres admin password and is not
> recoverable from Azure.

---

## 3. Pass 1 — base infra + registry

```bash
az deployment group create \
  -g "$RG" \
  -f main.bicep \
  -p main.bicepparam
```

Grab the registry name for the next step:

```bash
ACR=$(az deployment group show -g "$RG" -n main --query properties.outputs.acrName.value -o tsv)
echo "$ACR"
```

---

## 4. Build & push the images

Both images build **from the repo root** (the Go module and `shared/` must be in
context). Save these two Dockerfiles, then let ACR build them in the cloud.

**`control-plane/api/Dockerfile`**

```dockerfile
# Build from the REPO ROOT:  az acr build -f control-plane/api/Dockerfile .
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY shared ./shared
COPY control-plane ./control-plane
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/api ./control-plane/api/cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
```

**`web/Dockerfile`** — requires `output: 'standalone'` in `web/next.config.*`:

```dockerfile
FROM node:20-alpine AS deps
WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN npm ci

FROM node:20-alpine AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY web/ ./
RUN npm run build

FROM node:20-alpine AS run
WORKDIR /app
ENV NODE_ENV=production
ENV PORT=3000
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static
COPY --from=build /app/public ./public
EXPOSE 3000
CMD ["node", "server.js"]
```

Build both in ACR (run from the repo root):

```bash
az acr build -r "$ACR" -t cortex-api:latest     -f control-plane/api/Dockerfile .
az acr build -r "$ACR" -t cortex-console:latest -f web/Dockerfile .
```

---

## 5. Pass 2 — deploy the apps

```bash
az deployment group create \
  -g "$RG" -f main.bicep -p main.bicepparam \
  -p deployApps=true
```

Read the default FQDNs and the domain-verification ID (used for DNS):

```bash
az deployment group show -g "$RG" -n main --query properties.outputs -o json
CONSOLE_FQDN=$(az deployment group show -g "$RG" -n main --query properties.outputs.consoleDefaultFqdn.value -o tsv)
API_FQDN=$(az deployment group show -g "$RG" -n main --query properties.outputs.apiDefaultFqdn.value -o tsv)

# asuid verification ID (identical for both apps in this subscription)
ASUID=$(az containerapp show -g "$RG" -n cortex-cp-console --query properties.customDomainVerificationId -o tsv)
echo "console: $CONSOLE_FQDN"; echo "api: $API_FQDN"; echo "asuid: $ASUID"
```

At this point both apps are live on their `*.azurecontainerapps.io` FQDNs — a
good moment to smoke-test before wiring DNS:

```bash
curl -s "https://$API_FQDN/healthz"    # -> ok
```

---

## 6. DNS — `catalyst.sincs.dev`

At your `sincs.dev` DNS provider, add four records (values from Step 5).
`catalyst` and `api.catalyst` are subdomains, so both use **CNAME** (no apex A
record needed):

| Type | Name | Value |
| --- | --- | --- |
| CNAME | `catalyst` | `$CONSOLE_FQDN` |
| TXT | `asuid.catalyst` | `$ASUID` |
| CNAME | `api.catalyst` | `$API_FQDN` |
| TXT | `asuid.api.catalyst` | `$ASUID` |

Wait for propagation before Pass 3:

```bash
dig +short catalyst.sincs.dev
dig +short TXT asuid.catalyst.sincs.dev
dig +short api.catalyst.sincs.dev
dig +short TXT asuid.api.catalyst.sincs.dev
```

---

## 7. Pass 3 — bind the custom domains

Issues a free managed certificate for each host and binds it (validated via the
CNAME you just created):

```bash
az deployment group create \
  -g "$RG" -f main.bicep -p main.bicepparam \
  -p deployApps=true -p bindCustomDomains=true
```

Certificate issuance takes a few minutes. Verify:

```bash
curl -s https://api.catalyst.sincs.dev/healthz    # -> ok
open https://catalyst.sincs.dev                    # sign in with Entra
```

---

## 8. Post-deploy wiring

- **Entra redirect URI** — confirm `https://catalyst.sincs.dev/api/auth/callback/microsoft-entra-id`
  is present on the app registration (Step 1.5).
- **Point reconcilers at production** — when installing the reconciler managed app
  (`reconciler/infra/`), set
  `controlPlaneUrl=https://api.catalyst.sincs.dev` and
  `cortexApiScope=api://<client-id>/.default`.
- **First sign-in** — the API auto-migrates its schema on boot (`SEED_DEMO=false`,
  so no demo data). Sign in from your `CORTEX_PLATFORM_TENANT_ID` to land as a
  Platform Admin; the Fleet starts empty until a tenant enrolls.

---

## Configuration reference

Injected by `main.bicep`; app defaults come from
`control-plane/api/internal/config/config.go` and the console's `auth.ts`.

**API (`cortex-cp-api`)**

| Env | Value |
| --- | --- |
| `PORT` | `8080` |
| `DATABASE_URL` | secret — `postgres://…@…:5432/cortex?sslmode=require` |
| `ENTRA_CLIENT_ID` | `entraClientId` |
| `ENTRA_API_AUDIENCE` | `api://<client-id>` |
| `PLATFORM_TENANT_ID` | `platformTenantId` |
| `CORS_ORIGIN` | `https://catalyst.sincs.dev` |
| `SEED_DEMO` | `false` |

**Console (`cortex-cp-console`)**

| Env | Value |
| --- | --- |
| `AUTH_URL` | `https://catalyst.sincs.dev` |
| `AUTH_TRUST_HOST` | `true` |
| `AUTH_SECRET` | secret |
| `AUTH_MICROSOFT_ENTRA_ID_ID` | `entraClientId` |
| `AUTH_MICROSOFT_ENTRA_ID_SECRET` | secret |
| `AUTH_MICROSOFT_ENTRA_ID_ISSUER` | `https://login.microsoftonline.com/common/v2.0` |
| `PLATFORM_TENANT_ID` | `platformTenantId` |
| `CORTEX_API_URL` | `https://api.catalyst.sincs.dev` |
| `NEXT_PUBLIC_CORTEX_ENV` | `production` |

---

## Updating a service

Rebuild the image and restart the revision (single-revision mode picks up
`:latest`):

```bash
az acr build -r "$ACR" -t cortex-api:latest -f control-plane/api/Dockerfile .
az containerapp revision restart -g "$RG" -n cortex-cp-api \
  --revision "$(az containerapp revision list -g "$RG" -n cortex-cp-api --query '[0].name' -o tsv)"
```

Prefer immutable tags (`cortex-api:<git-sha>`) in production and pass
`-p imageTag=<git-sha>` on Pass 2/3 for auditable rollouts.

---

## Hardening (recommended next steps)

This template optimizes for a clean first deployment. For production, layer on:

- **Private networking** — VNet-inject the managed environment and give Postgres
  a private endpoint (drop `publicNetworkAccess`/the `AllowAzureServices` rule).
- **Secrets in Key Vault** — replace the inline container-app secrets with Key
  Vault references via the user-assigned identity.
- **PostgreSQL HA + geo-redundant backups**, and a larger non-Burstable SKU.
- **Least-privilege DB role** for the app instead of the server admin login.

---

## Teardown

```bash
az group delete -n "$RG" --yes --no-wait
```

Then remove the four `catalyst` DNS records from `sincs.dev`.
