# Cortex Control Plane — Microsoft Entra ID app registration

This guide creates the **one** Microsoft Entra ID (Azure AD) app registration that
powers Cortex sign-in and API authorization. Follow it exactly; the values it
produces map 1:1 to the environment variables in `web/.env.local` and
`control-plane/.env`.

The app registration plays two roles at once:

1. **OIDC client** — the Next.js console signs users in with it (multi-tenant, so
   users from any Microsoft Entra directory can sign in).
2. **Protected API** — it exposes a delegated scope (`access_as_user`). The console
   requests that scope, Entra mints an **access token for the API**, and the Go
   control-plane API validates it (signature via JWKS, audience, per-tenant issuer,
   and the scope).

You only create **one** app registration. Do it in the Entra tenant you will
operate Cortex from — that tenant becomes the **platform** tenant (its users are
Platform Admins; everyone else is a Tenant Admin scoped to their own tenant).

---

## What you'll collect

By the end you'll have four values:

| Value | Where it comes from | Goes into |
|---|---|---|
| **Application (client) ID** | App registration → Overview | `AUTH_MICROSOFT_ENTRA_ID_ID` (web) **and** `ENTRA_CLIENT_ID` (api) |
| **Directory (tenant) ID** | App registration → Overview | `PLATFORM_TENANT_ID` (web **and** api) |
| **Client secret (Value)** | Certificates & secrets | `AUTH_MICROSOFT_ENTRA_ID_SECRET` (web) |
| **Scope name** = `access_as_user` | You define it (fixed) | already the API default |

`AUTH_SECRET`, `AUTH_URL`, `NEXT_PUBLIC_CORTEX_ENV`, `CORTEX_API_URL` are already
set for local dev — don't touch them.

---

## Fixed values used below

| Thing | Value (copy exactly) |
|---|---|
| Redirect URI (Web) | `http://localhost:4200/api/auth/callback/microsoft-entra-id` |
| Application ID URI | `api://<APPLICATION_CLIENT_ID>` |
| Exposed scope | `access_as_user` |
| Sign-in audience | **Accounts in any organizational directory** (multitenant) |

> The redirect URI is case- and character-exact — `http` (not `https`) for
> localhost, no trailing slash, provider segment `microsoft-entra-id`.

---

## Option A — Azure Portal (click-through)

Portal: <https://entra.microsoft.com> → **Applications → App registrations**.

### A1. Register the application

1. **New registration.**
2. **Name:** `Cortex Control Plane`.
3. **Supported account types:** *Accounts in any organizational directory (Any
   Microsoft Entra ID tenant — Multitenant)*.
4. **Redirect URI:** platform **Web**, value
   `http://localhost:4200/api/auth/callback/microsoft-entra-id`.
5. **Register.**
6. On **Overview**, copy **Application (client) ID** and **Directory (tenant) ID**.

### A2. Add a client secret

1. **Certificates & secrets → Client secrets → New client secret.**
2. Description `cortex-local`, expiry your choice (e.g. 6 months).
3. **Add**, then copy the **Value** immediately (it's shown once). This is
   `AUTH_MICROSOFT_ENTRA_ID_SECRET`.

### A3. Expose the API scope

1. **Expose an API → Application ID URI → Add** → accept the default
   `api://<client-id>` → **Save**.
2. **Add a scope:**
   - **Scope name:** `access_as_user` (exactly).
   - **Who can consent:** *Admins and users*.
   - **Admin consent display name:** `Access Cortex as you`.
   - **Admin consent description:** `Allow the Cortex console to call the Cortex control-plane API as the signed-in user.`
   - **State:** Enabled → **Add scope.**

### A4. Let the console call its own API without prompting

Still on **Expose an API → Authorized client applications → Add a client
application:**

1. **Client ID:** paste this **same** Application (client) ID.
2. Tick the `access_as_user` scope → **Add application.**

This pre-authorizes the console (same app) so your platform-tenant users aren't
prompted to consent. (Customer tenants consent once, at first sign-in — see
[Multi-tenant consent](#multi-tenant-consent).)

### A5. (Optional) return `email` in the token

`API permissions` already includes `User.Read`/OIDC scopes. If you want the
`email` claim populated (Cortex falls back to `preferred_username` otherwise):
**Token configuration → Add optional claim → ID → `email` → Add.**

Skip to [Wire the env files](#wire-the-env-files).

---

## Option B — `az` CLI (equivalent, scripted)

You're already logged in (`az account show`). Run from a shell with `jq` and
`uuidgen` available. This does everything Option A does.

```bash
# --- create the multitenant app with the web redirect URI ---
APP_JSON=$(az ad app create \
  --display-name "Cortex Control Plane" \
  --sign-in-audience AzureADMultipleOrgs \
  --web-redirect-uris "http://localhost:4200/api/auth/callback/microsoft-entra-id")

APP_ID=$(echo "$APP_JSON"   | jq -r .appId)   # Application (client) ID
OBJ_ID=$(echo "$APP_JSON"   | jq -r .id)      # directory object id (for Graph)
TENANT_ID=$(az account show --query tenantId -o tsv)   # your platform tenant
SCOPE_ID=$(uuidgen)

echo "client id     : $APP_ID"
echo "tenant id     : $TENANT_ID"

# --- set the Application ID URI ---
az ad app update --id "$APP_ID" --identifier-uris "api://$APP_ID"

# --- add the access_as_user scope AND pre-authorize the app to call itself ---
az rest --method PATCH \
  --uri "https://graph.microsoft.com/v1.0/applications/$OBJ_ID" \
  --headers "Content-Type=application/json" \
  --body "{
    \"api\": {
      \"oauth2PermissionScopes\": [{
        \"id\": \"$SCOPE_ID\",
        \"value\": \"access_as_user\",
        \"type\": \"User\",
        \"isEnabled\": true,
        \"adminConsentDisplayName\": \"Access Cortex as you\",
        \"adminConsentDescription\": \"Allow the Cortex console to call the Cortex control-plane API as the signed-in user.\",
        \"userConsentDisplayName\": \"Access Cortex as you\",
        \"userConsentDescription\": \"Allow the Cortex console to call the Cortex control-plane API on your behalf.\"
      }],
      \"preAuthorizedApplications\": [{
        \"appId\": \"$APP_ID\",
        \"delegatedPermissionIds\": [\"$SCOPE_ID\"]
      }]
    }
  }"

# --- ensure a service principal exists in your tenant ---
az ad sp create --id "$APP_ID" 2>/dev/null || true

# --- create a client secret (shown once) ---
SECRET=$(az ad app credential reset --id "$APP_ID" \
  --display-name "cortex-local" --years 1 --query password -o tsv)

echo
echo "AUTH_MICROSOFT_ENTRA_ID_ID  / ENTRA_CLIENT_ID = $APP_ID"
echo "PLATFORM_TENANT_ID                            = $TENANT_ID"
echo "AUTH_MICROSOFT_ENTRA_ID_SECRET                = $SECRET"
```

> `az ad app credential reset` **replaces** existing secrets. On a brand-new app
> that's fine.

---

## Wire the env files

### `web/.env.local`

```dotenv
AUTH_MICROSOFT_ENTRA_ID_ID=<Application (client) ID>
AUTH_MICROSOFT_ENTRA_ID_SECRET=<client secret Value>
PLATFORM_TENANT_ID=<Directory (tenant) ID>
# leave these as-is:
# AUTH_MICROSOFT_ENTRA_ID_ISSUER=https://login.microsoftonline.com/common/v2.0
# AUTH_SECRET / AUTH_URL / NEXT_PUBLIC_CORTEX_ENV / CORTEX_API_URL
```

### `control-plane/.env`

```dotenv
ENTRA_CLIENT_ID=<Application (client) ID>     # SAME value as web
PLATFORM_TENANT_ID=<Directory (tenant) ID>    # SAME value as web
# ENTRA_REQUIRED_SCOPE defaults to access_as_user — no need to set it.
```

Both files are git-ignored. `ENTRA_CLIENT_ID` and `PLATFORM_TENANT_ID` **must
match** between the two files.

---

## Run it

Two long-running processes (Postgres must be running):

```bash
# terminal 1 — control-plane API (:8080)
cd control-plane && go run ./cmd/api

# terminal 2 — console (:4200)
cd web && npm run dev
```

Open <http://localhost:4200> → you're redirected to `/signin` → **Sign in with
Microsoft** → complete the Entra flow.

**What success looks like**
- Sign in from your **platform tenant** → **Platform Admin**: the Fleet page with
  the seeded tenants.
- Sign in from **any other organization** → **Tenant Admin**: an Overview scoped to
  that org's own (just-created) tenant, showing "Not installed yet".

---

## How the pieces map to the code

| App-registration setting | Enforced by |
|---|---|
| Multitenant + redirect URI | `web/auth.ts` (Auth.js `microsoft-entra-id` provider, `/common/v2.0`) |
| Client id + secret | `web/.env.local` → Auth.js confidential client |
| `api://<id>/access_as_user` scope requested | `web/auth.ts` `authorization.params.scope` (+ `offline_access` for refresh) |
| Access token audience (`aud` == client id or `api://<id>`) | `control-plane/internal/auth/auth.go` |
| Scope present (`scp` contains `access_as_user`) | `control-plane/internal/auth/auth.go` |
| Signed by Entra (RS256 via JWKS) | `control-plane/internal/auth/entra.go` |
| Per-tenant issuer + `tid` → role | `auth.go` + `PLATFORM_TENANT_ID` |

The console **never** puts a token in the browser: the access token lives in the
encrypted, httpOnly session cookie; the Next server reads it, refreshes it when it
expires, and forwards it to the Go API server-side.

---

## Multi-tenant consent

Because the app is multitenant, when a user from a **different** organization signs
in for the first time, Entra shows a consent screen for the requested permissions
(sign-in + `access_as_user`). Depending on that org's policy, an **admin** of that
org may need to consent once (admin-consent) before regular users can sign in. This
is standard multi-tenant behavior — nothing to configure on your side.

Your own **platform tenant** won't prompt, thanks to the "Authorized client
applications" pre-authorization (A4 / the CLI `preAuthorizedApplications`).

---

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `AADSTS50011: redirect URI … does not match` | The redirect URI isn't exactly `http://localhost:4200/api/auth/callback/microsoft-entra-id`. Fix it under **Authentication → Web**. |
| Redirected straight back to `/signin` after login | The API rejected the token. Check `ENTRA_CLIENT_ID` (api) == `AUTH_MICROSOFT_ENTRA_ID_ID` (web), and that the `access_as_user` scope exists and is requested. API logs (`go run`) show the exact reason (`wrong audience` / `missing required scope` / `issuer / tenant mismatch`). |
| `401 missing required scope` in API logs | The access token has no `access_as_user` scope. Confirm A3 (scope added) **and** A4 (pre-authorized) — or set `ENTRA_REQUIRED_SCOPE=` (empty) in `api/.env` to disable the check while debugging. |
| `AADSTS650051 / invalid scope` at sign-in | The Application ID URI or scope isn't set yet (A3), or the client id in the scope string is wrong. |
| Everyone is a Tenant Admin | `PLATFORM_TENANT_ID` doesn't equal your sign-in tenant. Copy the **Directory (tenant) ID** from the app Overview into both env files. |
| `AADSTS700016: application … not found in directory` | The service principal isn't in the tenant. Run `az ad sp create --id <APP_ID>` (or sign in once to auto-create it). |
| Warnings `ENTRA_CLIENT_ID not set` / `PLATFORM_TENANT_ID not set` on API start | You haven't filled `control-plane/.env` yet. |

---

## Going to production later

When you deploy, add the production redirect URI and set `AUTH_URL`:

- Entra → **Authentication → Web → Add URI**:
  `https://<your-console-domain>/api/auth/callback/microsoft-entra-id`
- `web/.env` (prod): `AUTH_URL=https://<your-console-domain>` and set
  `NEXT_PUBLIC_CORTEX_ENV=prod`.
- Rotate the client secret out of source and into your secret store; consider a
  **certificate** credential instead of a secret for the confidential client.
