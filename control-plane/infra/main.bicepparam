// Parameters for main.bicep. Non-secret config lives here; secrets are read
// from the operator's shell environment at compile time so they never land in
// source control. Export them before deploying (see DEPLOYMENT.md), e.g.:
//
//   export CORTEX_ENTRA_CLIENT_ID=...        CORTEX_PLATFORM_TENANT_ID=...
//   export CORTEX_PG_ADMIN_PASSWORD=...      CORTEX_AUTH_SECRET=...
//   export CORTEX_ENTRA_CLIENT_SECRET=...
//
// Set deployApps=true on the CLI for pass 2, e.g.:
//   az deployment group create ... --parameters main.bicepparam --parameters deployApps=true

using './main.bicep'

param consoleDomain = 'catalyst.msft.ae'
param apiDomain = 'api.catalyst.msft.ae'

// Pass 1 default; set deployApps=true on the CLI for pass 2.
param deployApps = false

param imageTag = 'latest'
param cortexEnv = 'prod'

param entraClientId = readEnvironmentVariable('CORTEX_ENTRA_CLIENT_ID', '')
param platformTenantId = readEnvironmentVariable('CORTEX_PLATFORM_TENANT_ID', '')

param postgresAdminPassword = readEnvironmentVariable('CORTEX_PG_ADMIN_PASSWORD', '')
param authSecret = readEnvironmentVariable('CORTEX_AUTH_SECRET', '')
param entraClientSecret = readEnvironmentVariable('CORTEX_ENTRA_CLIENT_SECRET', '')

// Optional: your public IP, to reach Postgres with psql for inspection.
param operatorIp = readEnvironmentVariable('CORTEX_OPERATOR_IP', '')

// ── Settings that were only ever passed on the CLI ───────────────────────────
// These have real values in the running deployment but no entry here, so an
// apply from this file would quietly reset them to the template's defaults —
// turning off cross-tenant provisioning, repointing the reconciler image, and
// (worst) blanking platformAdminEmails, which makes every user in the directory
// a platform admin. Pin them so an apply reproduces what is actually running.
param crossTenantProvisioning = true
param platformSubscriptionId = readEnvironmentVariable('CORTEX_PLATFORM_SUBSCRIPTION_ID', '')
param platformAdminEmails = readEnvironmentVariable('CORTEX_PLATFORM_ADMIN_EMAILS', '')
param reconcilerImage = readEnvironmentVariable('CORTEX_RECONCILER_IMAGE', '')
param footprintResourceGroup = 'cortex'
param infraResourceGroup = 'cortex-infra'
param acmeEmail = readEnvironmentVariable('CORTEX_ACME_EMAIL', '')

// The platform registry: which prefixes a tenant token may pull, and the
// control plane's own token for inspecting cached charts and modules.
param platformAcrRepos = 'charts/*,bicep/*,images/*'
param platformAcrPullUser = readEnvironmentVariable('CORTEX_ACR_PULL_USER', '')
param platformAcrPullPassword = readEnvironmentVariable('CORTEX_ACR_PULL_PASSWORD', '')

// Managed certificates for the custom domains. Issued once (they require DNS to
// already resolve to the app), then pinned here so an apply re-binds them
// instead of deleting the binding.
param apiCertificateId = readEnvironmentVariable('CORTEX_API_CERT_ID', '')
param consoleCertificateId = readEnvironmentVariable('CORTEX_CONSOLE_CERT_ID', '')
