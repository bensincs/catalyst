using './main.bicep'

// Provide the password out-of-band (never hard-code secrets):
//   export PG_ADMIN_PASSWORD='...'
//   az deployment group create -g <rg> -f examples/main.bicep -p examples/dev.bicepparam
param administratorLoginPassword = readEnvironmentVariable('PG_ADMIN_PASSWORD', '')
param environmentName = 'dev'
