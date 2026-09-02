# todo-app

A FastAPI + PostgreSQL todo service. The first platform service, and the one the
catalog's shape was worked out against — it exercises all three artifacts and the
full wiring path, including a credential that never appears in configuration.

## What it ships

| | |
|---|---|
| `app/` | FastAPI service, Dockerfile, pytest suite (runs against SQLite) |
| `chart/` | Helm chart `todo-app` |
| `infra/` | Bicep module for Azure Database for PostgreSQL Flexible Server |

## How it is registered

**Infrastructure** — the Bicep module, provisioned into the tenant's own
subscription. It exposes:

```
host  port  databaseName  administratorLogin  sslMode  serverId  serverName
```

Note what is *not* an output: the password. `administratorLoginPassword` is
`@secure()`, and a secure output would be published in cleartext into the
deployment's outputs and from there into the app's Helm values, so the module
does not emit it.

**Secret store** — declares one key, `password`. The platform declares the shape;
each tenant supplies its own value, which is written to that tenant's Key Vault
and delivered into the cluster as a Kubernetes Secret by the reconciler.

**Application** — the Helm chart, wired as:

| Source | Output | Helm path |
|---|---|---|
| infrastructure | `host` | `database.host` |
| infrastructure | `port` | `database.port` |
| infrastructure | `administratorLogin` | `database.user` |
| infrastructure | `databaseName` | `database.name` |
| infrastructure | `sslMode` | `database.sslMode` |
| secret store | `secretName` | `database.existingSecret` |
| secret store | `key:password` | `database.existingSecretPasswordKey` |

The last two are the point. Both are **names**: which Kubernetes Secret to read
and which key inside it. The chart resolves them at runtime via
`secretKeyRef`, so the credential never enters the values, the Argo Application,
or the control plane's database.

The chart supports either style — set `database.password` and it creates its own
Secret, or set `database.existingSecret` and it reads one. The platform always
uses the second.

## Local development

```sh
cd app && uv sync && uv run pytest      # tests run against SQLite
helm lint ../chart --set database.host=h --set database.user=u \
                   --set database.existingSecret=s
az bicep build --file ../infra/main.bicep --stdout > /dev/null
```
