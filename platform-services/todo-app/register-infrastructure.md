# Registering the Postgres module — an open decision

`register.json` deliberately omits the infrastructure entry. This is why.

## The problem

`infra/main.bicep` declares:

```bicep
@secure()
@minLength(8)
param administratorLoginPassword string      // required — no default
```

Secret stores bind to Helm charts only, and `Resolve` bakes every author-supplied
parameter into `arm_template` as a literal. So registering this module today
means putting the password in `bicepParams`, where it is:

- stored in the control plane's database,
- compiled into `arm_template`,
- and preserved in that tenant's Azure deployment history permanently,
  readable by anyone with reader access on the resource group.

That is exactly the exposure secret stores exist to remove. Registering it that
way would undo the work rather than use it.

## Why the obvious fixes do not work

**Give the parameter a generated default.** `@secure() param … = '${uniqueString(...)}Aa1!'`
compiles the *expression* rather than a value, and ARM redacts secure parameters
from deployment history — so nothing leaks. But the application then has no way
to learn the password, because it was derived inside ARM.

**Have the tenant supply the same password to both.** The secret store already
collects it. The module cannot receive it: passing a parameter at deploy time was
removed along with the Bicep binding, and the control plane cannot read the
tenant's vault to fetch it even if it wanted to.

## The three real options

**1. The module generates the credential and writes it to the tenant's vault.**
Take a (non-secret) `vaultName` parameter, generate the password, and write it as
a Key Vault secret named the way the secret store expects. The reconciler already
reads that vault, so delivery works unchanged. Needs one control-plane change:
a secret store key must be able to be satisfied by infrastructure rather than
only by the tenant, or the application stays held waiting for a value that has
already been written.

*Fits the existing machinery best. Smallest change that is actually correct.*

**2. Passwordless — Entra authentication.** Set
`authConfig: { activeDirectoryAuth: 'Enabled', passwordAuth: 'Disabled' }` on the
server, grant the workload's identity, and have the app fetch a token instead of
a password. There is then no credential anywhere: no secret store, no vault
entry, nothing to rotate or leak. Requires changes to the app (token as
password, refreshed) and workload identity federation for the pod.

*The best answer, and the most work.*

**3. Provision the database outside the catalog.** Register only the application
and the secret store; someone creates the server by hand and supplies host and
password. Unblocks today, but the point of the catalog is that a tenant does not
do this.

## Recommendation

Option 1 to unblock, with option 2 as the direction. Option 1 keeps the
credential out of every place it currently leaks and reuses the delivery path
that already works; option 2 removes the credential from existence, which is
strictly better but is a change to the application, not just to packaging.

Until one is chosen, `register.json` registers the application and its secret
store, and `database.host` is a placeholder pointing at a manually-created
server.
