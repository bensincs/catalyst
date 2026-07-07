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

param consoleDomain = 'catalyst.sincs.dev'
param apiDomain = 'api.catalyst.sincs.dev'

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
