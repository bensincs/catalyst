# CI/CD

GitHub Actions for the whole repo.

| Workflow | Trigger | What it does |
|---|---|---|
| **ci.yml** | every push + PR | `go build/vet/test ./...` (control-plane store tests run against a Postgres service), `staticcheck` (advisory), and the web `typecheck` + `next build`. |
| **deploy.yml** | push to `main`/`feat/k8s-gitops-mesh` (excludes reconciler/cli/docs), or manual | Builds `cortex-api` + `cortex-console` in ACR at the short git SHA and rolls the Container Apps to it (in parallel). |
| **deploy-reconciler.yml** | push touching `reconciler/**` or `shared/**`, or manual | Builds the reconciler, imports it to the **public** (anonymous-pull) registry. Only re-points tenants at it (`RECONCILER_IMAGE`) when you run it manually with **promote = true**. |
| **release-cli.yml** | tag `cortexctl-v*`, or manual | Cross-compiles `cortexctl` (linux/darwin/windows × amd64/arm64) and attaches the binaries + checksums to a GitHub Release. |

## Azure auth (one-time)

Deploys authenticate to Azure with **OIDC** — no stored passwords. Provision the
federated identity and repo secrets once:

```bash
./.github/scripts/azure-oidc-setup.sh <owner>/<repo>
```

It creates an Entra app + federated credentials for the deployable branches,
grants Contributor on the resource group and registries, and sets three repo
secrets: `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_SUBSCRIPTION_ID`.

Infrastructure names are baked in as defaults but overridable with repository
**variables**: `AZURE_RESOURCE_GROUP`, `ACR_NAME`, `PUBLIC_ACR_NAME`, `API_APP`.

**Secret-based fallback:** if you can't use OIDC, create one `AZURE_CREDENTIALS`
secret (`az ad sp create-for-rbac --sdk-auth …`) and change the `azure/login`
steps to `with: { creds: ${{ secrets.AZURE_CREDENTIALS }} }`.
