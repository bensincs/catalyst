# PostgreSQL Flexible Server — Bicep module

Provisions an [Azure Database for PostgreSQL Flexible Server][flex], an
application database, and firewall rules. Its outputs are shaped so a
provisioning platform can feed them straight into the `todo-app` Helm chart.

## Files

| File                     | Purpose                                             |
| ------------------------ | --------------------------------------------------- |
| `main.bicep`             | The reusable module (publish this to GHCR).         |
| `examples/main.bicep`    | Consumes the module by path; compiles locally.      |
| `examples/dev.bicepparam`| Example parameter file.                             |
| `../bicepconfig.json`    | Declares the `br/ghcr` module alias for GHCR.       |

## Parameters (most common)

| Name                         | Type   | Default            | Notes                                    |
| ---------------------------- | ------ | ------------------ | ---------------------------------------- |
| `name`                       | string | —                  | Server name (3–63 chars).                |
| `location`                   | string | RG location        |                                          |
| `administratorLogin`         | string | `todoadmin`        |                                          |
| `administratorLoginPassword` | secure | —                  | **Required.** Never emitted as output.   |
| `skuName` / `skuTier`        | string | `Standard_B1ms` / `Burstable` |                               |
| `postgresVersion`            | string | `16`               | 13–17.                                   |
| `storageSizeGB`              | int    | `32`               |                                          |
| `databaseName`               | string | `todos`            |                                          |
| `allowAzureServices`         | bool   | `true`             | Adds the 0.0.0.0 firewall rule.          |
| `firewallRules`              | array  | `[]`               | `[{ name, startIpAddress, endIpAddress }]` |
| `highAvailabilityMode`       | string | `Disabled`         | `Disabled` / `ZoneRedundant` / `SameZone`|

See `main.bicep` for the full list, allowed values and validation.

## Outputs → Helm values

| Output               | Type   | Helm value (`todo-app`)   |
| -------------------- | ------ | ------------------------- |
| `host`               | string | `database.host`           |
| `port`               | int    | `database.port`           |
| `databaseName`       | string | `database.name`           |
| `administratorLogin` | string | `database.user`           |
| `sslMode`            | string | `database.sslMode`        |

`serverId` and `serverName` are also emitted but are informational only (not
consumed by the chart).

The **password is deliberately not an output** (secrets must not be emitted).
Supply it to the Helm chart separately via `database.password` or
`database.existingSecret`.

See [`../../../docs/wiring.md`](../../../docs/wiring.md) for the end-to-end flow.

## Validate / build locally

```bash
az bicep build --file main.bicep
az bicep build --file examples/main.bicep
```

## Deploy directly (optional, outside the platform)

```bash
export PG_ADMIN_PASSWORD='<strong-password>'
az group create -n rg-todo -l uksouth
az deployment group create \
  -g rg-todo \
  -f examples/main.bicep \
  -p examples/dev.bicepparam
```

## Publish to GHCR

> **Why not `az bicep publish`?** Bicep's native publish targets **Azure
> Container Registry** — it authenticates with Azure AD, so it cannot push to
> GHCR (you'll get a `ChainedTokenCredential`/"run az login" error). Instead we
> push the module to GHCR as an OCI artifact with [`oras`], using Bicep's media
> types so the result is a valid Bicep module artifact.

The `publish` GitHub Actions workflow does this automatically. To publish from
your machine (`make bicep-publish`, or by hand):

```bash
echo "$GITHUB_TOKEN" | oras login ghcr.io -u bensincs --password-stdin

az bicep build --file main.bicep --outfile main.json
printf '{}' > config.json

oras push \
  --config config.json:application/vnd.ms.bicep.module.config.v1+json \
  ghcr.io/bensincs/bicep/postgres:0.1.0 \
  main.json:application/vnd.ms.bicep.module.layer.v1+json
```

## Consume the published module

Pull it anywhere (the platform that wires the outputs does this):

```bash
oras pull ghcr.io/bensincs/bicep/postgres:0.1.0   # -> main.json (compiled ARM)
```

Bicep's `br/ghcr:postgres:0.1.0` alias (see `../bicepconfig.json`) is set up for
`ghcr.io/bensincs/bicep`; anonymous `bicep restore` from GHCR works only when the
package is **public** and your Bicep version supports anonymous OCI pulls. If you
need first-class `br:` restore, mirror the module to an Azure Container Registry.

> Make the GHCR package **public** in your GitHub package settings for anonymous
> pulls.

[`oras`]: https://oras.land/

[flex]: https://learn.microsoft.com/azure/postgresql/flexible-server/
