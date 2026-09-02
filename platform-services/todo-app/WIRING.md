# Wiring: Bicep outputs → Helm inputs

This is the contract your provisioning platform implements: run the Bicep
module, take its outputs, and pass them as `todo-app` Helm chart values. The
password is the only value that does not come from a Bicep output.

```
┌────────────────────────┐        outputs         ┌──────────────────────────┐
│  Bicep: postgres module │ ─────────────────────▶ │  Helm: todo-app values   │
│  (Flexible Server)      │                        │  database.*              │
└────────────────────────┘                        └──────────────────────────┘
            ▲                                                   ▲
            │ administratorLoginPassword (secure param)         │ database.password
            │                                                   │   or database.existingSecret
     platform-held secret ──────────────────────────────────────┘
```

## Mapping table

| Bicep module output  | Helm value          | Env var in the app  |
| -------------------- | ------------------- | ------------------- |
| `host`               | `database.host`     | `DATABASE_HOST`     |
| `port`               | `database.port`     | `DATABASE_PORT`     |
| `databaseName`       | `database.name`     | `DATABASE_NAME`     |
| `administratorLogin` | `database.user`     | `DATABASE_USER`     |
| `sslMode`            | `database.sslMode`  | `DATABASE_SSLMODE`  |
| _(secret, not output)_ | `database.password` / `database.existingSecret` | `DATABASE_PASSWORD` |

Each row is a separate Bicep output, so your platform maps them one-to-one onto
the chart values.

## Reference flow (manual equivalent of what the platform automates)

### 1. Provision Postgres and capture outputs

```bash
export PG_ADMIN_PASSWORD='<strong-password>'

OUT=$(az deployment group create \
  -g rg-todo \
  -f infra/bicep/postgres/examples/main.bicep \
  -p infra/bicep/postgres/examples/dev.bicepparam \
  --query properties.outputs -o json)

DB_HOST=$(echo "$OUT" | jq -r '.host.value')
DB_PORT=$(echo "$OUT" | jq -r '.port.value')
DB_NAME=$(echo "$OUT" | jq -r '.databaseName.value')
DB_USER=$(echo "$OUT" | jq -r '.administratorLogin.value')
DB_SSL=$(echo  "$OUT" | jq -r '.sslMode.value')
```

### 2a. Deploy the chart, chart-managed secret

```bash
helm upgrade --install todo \
  oci://ghcr.io/bensincs/charts/todo-app --version 0.1.0 \
  --set image.repository=ghcr.io/bensincs/todoapp \
  --set database.host="$DB_HOST" \
  --set database.port="$DB_PORT" \
  --set database.name="$DB_NAME" \
  --set database.user="$DB_USER" \
  --set database.sslMode="$DB_SSL" \
  --set database.password="$PG_ADMIN_PASSWORD"
```

### 2b. Deploy the chart, pre-created secret (recommended for platforms)

```bash
kubectl create secret generic todo-db \
  --from-literal=password="$PG_ADMIN_PASSWORD"

helm upgrade --install todo \
  oci://ghcr.io/bensincs/charts/todo-app --version 0.1.0 \
  --set image.repository=ghcr.io/bensincs/todoapp \
  --set database.host="$DB_HOST" \
  --set database.port="$DB_PORT" \
  --set database.name="$DB_NAME" \
  --set database.user="$DB_USER" \
  --set database.sslMode="$DB_SSL" \
  --set database.existingSecret=todo-db \
  --set database.existingSecretPasswordKey=password
```

## Programmatic wiring (values file)

If your platform prefers a values file, map each Bicep output onto the matching
`database.*` field and add the password source:

```yaml
# values.generated.yaml (produced by the platform from Bicep outputs)
image:
  repository: ghcr.io/bensincs/todoapp
database:
  host: todo-pg-dev-abc123.postgres.database.azure.com
  port: 5432
  name: todos
  user: todoadmin
  sslMode: require
  existingSecret: todo-db          # platform created this secret
```

```bash
helm upgrade --install todo \
  oci://ghcr.io/bensincs/charts/todo-app --version 0.1.0 \
  -f values.generated.yaml
```

## Guarantees enforced by the chart

- `database.host` **must** be set, or templating fails with a clear message.
- A password source **must** be provided (`database.password` _or_
  `database.existingSecret`), or templating fails.
- `sslMode` defaults to `require` — correct for Azure Flexible Server.
