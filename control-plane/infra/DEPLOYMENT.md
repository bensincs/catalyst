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

## Why two passes + a bind step

An image must exist in the registry before an app can start, so the template
deploys in two passes gated by `deployApps`:

| Pass | `deployApps` | Creates |
| --- | --- | --- |
| 1 | `false` | registry, Postgres, env, identity |
| 2 | `true` | the two apps, on their default `*.azurecontainerapps.io` FQDNs |

Custom domains + managed certificates are then bound with the CLI (Step 7), not in
Bicep: Container Apps requires a hostname to be *added* before its managed cert can
be created — an ordering a single template pass can't express.

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

# Postgres goes into a postgres:// DSN, so the password must be URL-safe — base64url
# (A-Za-z0-9-_) keeps upper+lower+digit for PG complexity with no /, +, = to break the URL.
export CORTEX_PG_ADMIN_PASSWORD="$(openssl rand -base64 24 | tr '+/' '-_' | tr -d '=')"  # store it — Azure can't recover it
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

The repo ships both Dockerfiles: **`control-plane/Dockerfile`** (Go API, distroless)
and **`web/Dockerfile`** (Next.js standalone — `output: "standalone"` is already set
in `web/next.config.mjs`, and the authed routes are `force-dynamic` so the build
never needs the API). Both build **from the repo root** (the Go API needs the shared
module + root `go.mod`); a root `.dockerignore` keeps the context lean.

Build both in ACR, from the repo root:

```bash
az acr build -r "$ACR" -t cortex-api:latest     -f control-plane/Dockerfile .
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

Wait for propagation before binding:

```bash
dig +short catalyst.sincs.dev
dig +short TXT asuid.catalyst.sincs.dev
dig +short api.catalyst.sincs.dev
dig +short TXT asuid.api.catalyst.sincs.dev
```

---

## 7. Bind the custom domains (CLI)

DNS resolves, so add each hostname (validated via its `asuid` TXT) and issue a
free managed certificate. This is a CLI step, not a Bicep pass — Container Apps
won't create a managed cert for a hostname that hasn't been added first.

```bash
ENV=cortex-cp-env

# 1) add the hostnames
az containerapp hostname add -g "$RG" -n cortex-cp-console --hostname catalyst.sincs.dev
az containerapp hostname add -g "$RG" -n cortex-cp-api     --hostname api.catalyst.sincs.dev

# 2) issue + SNI-bind a managed cert for each (a few minutes each)
az containerapp hostname bind -g "$RG" -n cortex-cp-console \
  --hostname catalyst.sincs.dev --environment "$ENV" --validation-method CNAME
az containerapp hostname bind -g "$RG" -n cortex-cp-api \
  --hostname api.catalyst.sincs.dev --environment "$ENV" --validation-method CNAME
```

If a cert issues but the hostname stays `bindingType: Disabled`, attach the
existing cert by name:

```bash
CERT=$(az containerapp env certificate list -g "$RG" -n "$ENV" --managed-certificates-only \
  --query "[?properties.subjectName=='api.catalyst.sincs.dev'].name" -o tsv)
az containerapp hostname bind -g "$RG" -n cortex-cp-api \
  --hostname api.catalyst.sincs.dev --certificate "$CERT" --environment "$ENV"
```

Verify:

```bash
curl -s https://api.catalyst.sincs.dev/healthz    # -> {"status":"ok"}
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
`control-plane/internal/config/config.go` and the console's `auth.ts`.

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
| `NEXT_PUBLIC_CORTEX_ENV` | `prod` |

---

## Updating a service

Rebuild the image, then roll it onto the running app with `az containerapp update`
— **not** by re-running the template. A full app deploy PUTs the whole app and would
drop the CLI-bound custom domains.

```bash
az acr build -r "$ACR" -t cortex-console:latest -f web/Dockerfile .
az containerapp update -g "$RG" -n cortex-cp-console \
  --image "$ACR.azurecr.io/cortex-console:latest"
```

`az containerapp update --set-env-vars KEY=value` adjusts config without a rebuild.
Prefer immutable tags (`cortex-console:<git-sha>`) in production for auditable rollouts.

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
