# Authorization Service

Standalone SpiceDB-backed authorization service for gateway app access and
application-scoped roles/permissions.

The service can run independently from the rest of Cortex. Keep this directory
with its `Dockerfile`, `docker-compose.yaml`, `src/`, `schema/`, `config/`, and
`scripts/` folders and it can be built and run as a local authz stack for other
apps or platforms.

## Agent Quick Reference

Use this section when an AI coding agent needs to build on top of the service.

- Do not read app roles from OIDC `roles` or `groups` claims.
- Gateway/ext-authz access is coarse and binary:
  membership in any linked app role grants `application#can_access`.
- App/BFF runtime authorization uses `/v1/apps/{app}/permissions/check`.
- App/BFF role resolution/session metadata uses
  `/v1/apps/{app}/roles/list-for-subject`; cache it with a short TTL.
- Role and permission mutations use `/v1/apps/{app}/roles/...`.
- All app-specific roles and permissions are SpiceDB data, not schema changes.
- Do not add per-app schema relations like `relation analytics_admin: user`.
- Keep `application#accessor` only for schema compatibility with stale tuples;
  it grants no permission.
- App names follow the gateway host label shape: letters, numbers, `_`, `-`.
  Dots are reserved for role and permission names.
- Role membership directly grants gateway application access.
- Removing a user's final app role revokes gateway application access.

## Architecture

```text
authz-service (FastAPI, port 8080)
        |
        | gRPC
        v
SpiceDB (port 50051)
        |
        | PostgreSQL wire protocol
        v
PostgreSQL 16
```

## Standalone Local Run

Prerequisites: Docker.

From this directory:

```bash
docker compose up --build -d
```

This starts:

- `postgres`
- `spicedb-migrate`
- `spicedb`
- `spicedb-ready`
- `schema-load`
- `authz-service`

The compose stack loads `schema/cortex.zed` automatically and exposes:

- authz-service: `http://localhost:8080`
- SpiceDB gRPC: `localhost:50051`
- Postgres: `localhost:5432`

Smoke test:

```bash
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

Expected:

```json
{"status":"ok"}
```

```json
{"status":"ready","spicedb":"connected"}
```

Stop the stack:

```bash
docker compose down
```

Remove local authorization data too:

```bash
docker compose down -v
```

## Local Python Development

Run only the infrastructure services:

```bash
docker compose up --build -d postgres spicedb schema-load
```

Set the local service environment:

```bash
export SPICEDB_ENDPOINT=localhost:50051
export SPICEDB_PRESHARED_KEY=dev-key-not-for-production
export SPICEDB_INSECURE=true
export AUTHZ_ADMIN_TOKEN=dev-admin
export AUTHZ_CHECK_TOKEN=dev-check
```

Start the FastAPI service from Python:

```bash
uv run --extra dev uvicorn app.main:build_app --factory --reload \
  --host 0.0.0.0 \
  --port 8080
```

If `schema/cortex.zed` changes while SpiceDB is already running, reload it with:

```bash
uv run python scripts/load_spicedb_schema.py schema/cortex.zed
```

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `SPICEDB_ENDPOINT` | `localhost:50051` | SpiceDB gRPC endpoint. Use `spicedb:50051` inside Docker Compose. |
| `SPICEDB_PRESHARED_KEY` | required | Bearer token used by authz-service to call SpiceDB. |
| `SPICEDB_INSECURE` | `false` | Set to `true` for plaintext local SpiceDB. |
| `AUTHZ_ADMIN_TOKEN` | required for mutations | Bearer token for role/member/relationship write APIs. |
| `AUTHZ_CHECK_TOKEN` | falls back to `AUTHZ_ADMIN_TOKEN` | Lower-privilege bearer token for runtime permission checks. |
| `CORTEX_BOOTSTRAP_SIGNING_KEY` | unset | Optional HS256 signing key for bootstrap magic-link validation. |

## API Surface

Actual mounted routes:

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/health` | Process liveness. Does not require SpiceDB. |
| `GET` | `/ready` | Readiness. Requires SpiceDB connectivity and loaded schema. |
| `GET` | `/metrics` | Prometheus metrics. |
| `POST` | `/v1/admin/relationships` | Write raw SpiceDB relationships. Admin token required. |
| `GET` | `/v1/admin/relationships` | List tenant-scoped relationships. Admin token required. |
| `DELETE` | `/v1/admin/relationships` | Delete one raw relationship. Admin token required. |
| `GET` | `/v1/admin/schema/relations` | Return known relations for a resource type. Admin token required. |
| `GET` | `/v1/admin/subjects` | List user subjects with relationships in a tenant. Admin token required. |
| `PUT` | `/v1/apps/{app}/roles/{role}` | Idempotently ensure an app role exists (seeds the enumerable sentinel edge). Admin token required. |
| `GET` | `/v1/apps` | Enumerate the apps that have any seeded role in this tenant. Admin token required. |
| `GET` | `/v1/apps/{app}/roles` | Enumerate the app roles seeded for this tenant. Admin token required. |
| `POST` | `/v1/apps/{app}/roles/{role}/members` | Add a user to an app role and grant app access. Admin token required. |
| `DELETE` | `/v1/apps/{app}/roles/{role}/members` | Remove a user from an app role. Admin token required. |
| `GET` | `/v1/apps/{app}/roles/{role}/members` | List the subjects assigned to an app role. Admin token required. |
| `GET` | `/v1/apps/{app}/roles/{role}/permissions` | List the permission keys granted to a role (read-only taxonomy view). Admin token required. |
| `POST` | `/v1/apps/{app}/roles/{role}/permissions` | Grant an app permission to a role. Admin token required. |
| `DELETE` | `/v1/apps/{app}/roles/{role}/permissions` | Remove an app permission from a role. Admin token required. |
| `POST` | `/v1/apps/{app}/roles/list-for-subject` | List app roles assigned to a subject. Check token required. |
| `POST` | `/v1/apps/{app}/permissions/check` | Check whether a subject has an app permission. Check token required. |
| any | `/v1/ext-authz/check-oidc` | Gateway OIDC-mode authorization. |
| any | `/v1/ext-authz/check-bootstrap` | Gateway bootstrap-mode authorization. |
| any | `/v1/ext-authz/check` | Legacy gateway OIDC-mode authorization. |

The older README routes `/v1/permissions/*` and `/v1/roles/*` are not mounted by
the current service.

## App Roles And Permissions

Use app-scoped roles for permissions inside an application. Example roles:

- `viewer`
- `editor`
- `admin`

Example permissions:

- `dashboard.view`
- `report.manage`
- `dataset.write`

Object IDs are encoded before writing to SpiceDB:

```text
app_role:<app>|<role>_<tenant>
app_role_assignment:<app>|<subject>_<tenant>
app_permission:<app>|<permission>_<tenant>
```

Examples:

```text
app_role:analytics|admin_tenant_a
app_role_assignment:analytics|user_colon_alice_at_example_dot_com_tenant_a
app_permission:analytics|report_dot_manage_tenant_a
```

The public API accepts the readable names (`analytics`, `admin`,
`report.manage`). Callers should not pre-encode object IDs.

### Generic App Flow

Set variables for any application:

```bash
BASE_URL=http://localhost:8080
TENANT_ID=tenant-a
APP_ID=analytics
ROLE_ID=admin
PERMISSION=report.manage
SUBJECT=user:alice@example.com
ADMIN_TOKEN=dev-admin
CHECK_TOKEN=dev-check
```

Grant a permission to a role:

```bash
curl -s -X POST "$BASE_URL/v1/apps/$APP_ID/roles/$ROLE_ID/permissions" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "x-cortex-tenant: $TENANT_ID" \
  -H "Content-Type: application/json" \
  -d "{
    \"tenant_id\": \"$TENANT_ID\",
    \"permission\": \"$PERMISSION\"
  }"
```

Add a subject to the role:

```bash
curl -s -X POST "$BASE_URL/v1/apps/$APP_ID/roles/$ROLE_ID/members" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "x-cortex-tenant: $TENANT_ID" \
  -H "Content-Type: application/json" \
  -d "{
    \"tenant_id\": \"$TENANT_ID\",
    \"subject\": \"$SUBJECT\"
  }"
```

Check the permission at runtime:

```bash
curl -s -X POST "$BASE_URL/v1/apps/$APP_ID/permissions/check" \
  -H "Authorization: Bearer $CHECK_TOKEN" \
  -H "x-cortex-tenant: $TENANT_ID" \
  -H "Content-Type: application/json" \
  -d "{
    \"tenant_id\": \"$TENANT_ID\",
    \"permission\": \"$PERMISSION\",
    \"subject\": \"$SUBJECT\"
  }"
```

Allowed response:

```json
{
  "allowed": true,
  "checked_at": "zedtoken"
}
```

Negative checks return `200` with `"allowed": false`.

List the subject's app roles for BFF role resolution:

```bash
curl -s -X POST "$BASE_URL/v1/apps/$APP_ID/roles/list-for-subject" \
  -H "Authorization: Bearer $CHECK_TOKEN" \
  -H "x-cortex-tenant: $TENANT_ID" \
  -H "Content-Type: application/json" \
  -d "{
    \"tenant_id\": \"$TENANT_ID\",
    \"subject\": \"$SUBJECT\"
  }"
```

Response:

```json
{
  "roles": ["admin"]
}
```

Use this when a BFF needs to resolve a user's current app roles without making
one permission check per possible role. This endpoint is for role
resolution/session metadata, not final permission enforcement. Cache results
per `(tenant_id, subject)` with a short TTL. The endpoint returns all matching
roles; apps that need a primary role should apply their own priority order.

### Remove Grants

Remove a subject from a role:

```bash
curl -s -X DELETE "$BASE_URL/v1/apps/$APP_ID/roles/$ROLE_ID/members" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "x-cortex-tenant: $TENANT_ID" \
  -H "Content-Type: application/json" \
  -d "{
    \"tenant_id\": \"$TENANT_ID\",
    \"subject\": \"$SUBJECT\"
  }"
```

Remove a permission from a role:

```bash
curl -s -X DELETE "$BASE_URL/v1/apps/$APP_ID/roles/$ROLE_ID/permissions" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "x-cortex-tenant: $TENANT_ID" \
  -H "Content-Type: application/json" \
  -d "{
    \"tenant_id\": \"$TENANT_ID\",
    \"permission\": \"$PERMISSION\"
  }"
```

Removing a role member revokes the access contributed by that role. The user
retains gateway access while they remain a member of another role in the app.

## Gateway Integration

The gateway path is intentionally separate from app runtime permissions.

For OIDC tenants, Envoy should call:

```text
/v1/ext-authz/check-oidc
```

For bootstrap tenants, Envoy should call:

```text
/v1/ext-authz/check-bootstrap
```

A `?_bootstrap=` / `_bootstrap_token` credential on `/check-oidc` does not
grant access (Bearer only). If that bootstrap JWT's `tenant` claim does not
match the operator-stamped path tenant, the request is denied with 403 so a
magic link cannot be reused on a different tenant's OIDC gateway.

The host header must match:

```text
<app>.<tenant>.cortex.ai
```

The service checks:

```text
application:<app>@<tenant>#can_access
```

On allow, ext-authz forwards `x-cortex-sub`. It does not forward app roles.
Your app or BFF should call `/v1/apps/{app}/permissions/check` for final
permission decisions, or `/v1/apps/{app}/roles/list-for-subject` when it needs
the subject's app roles for session metadata.

## Admin Relationship API

Use `/v1/admin/relationships` for raw SpiceDB relationship operations. This is
useful for operators, migration scripts, and debugging.

Write raw relationships, for example linking an application to a role:

```bash
curl -s -X POST "$BASE_URL/v1/admin/relationships" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": "tenant-a",
    "relationships": [
      {
        "resource": "application:analytics@tenant-a",
        "relation": "role",
        "subject": "app_role:analytics|admin_tenant_a"
      }
    ]
  }'
```

List relationships:

```bash
curl -s "$BASE_URL/v1/admin/relationships?tenant_id=tenant-a" \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

## Schema Notes

Current app-access schema derives regular user access from role membership:

```zed
definition application {
    relation tenant: tenant
    relation accessor: user | bootstrap // legacy data only; grants nothing
    relation bootstrap_access: bootstrap
    relation role: app_role
    permission can_access = bootstrap_access + role->member
}
```

App roles and permissions are generic:

```zed
definition app_role {
    relation member: user
}

definition app_role_assignment {
    relation role: app_role
    permission assigned = role
}

definition app_permission {
    relation granted_to: app_role
    permission check = granted_to->member
}
```

To add a future application, do not edit the schema. Create role memberships and
permission grants through `/v1/apps/...`.

## Validation

Run unit tests:

```bash
uv run --extra dev pytest -q
```

Run focused authz checks:

```bash
uv run ruff check src/routes/app_permissions.py src/data/spicedb_client.py \
  src/data/models.py tests/conftest.py tests/test_app_permissions.py \
  tests/test_spicedb_client.py
uv run --extra dev pytest -q tests/test_app_permissions.py
```

Run standalone smoke tests:

```bash
docker compose up --build -d
curl http://localhost:8080/ready
docker compose down
```

## Troubleshooting

`/ready` returns `503`:

- Confirm `docker compose ps` shows `spicedb` running.
- Confirm `spicedb-ready` and `schema-load` completed successfully.
- Check `docker compose logs spicedb-ready schema-load authz-service spicedb`.

Permission check always returns `false`:

- Confirm the permission was granted to the role.
- Confirm the subject was added to the same role.
- Confirm `tenant_id` and `x-cortex-tenant` match.
- Confirm the app, role, and permission names are the same strings used during
  grant and check.

Mutation returns `403`:

- Use `Authorization: Bearer <AUTHZ_ADMIN_TOKEN>`.

Runtime check returns `403`:

- Use `Authorization: Bearer <AUTHZ_CHECK_TOKEN>`.
- If no check token is configured, use `AUTHZ_ADMIN_TOKEN` only for local
  development.

Invalid app name returns `422`:

- App names may contain letters, numbers, `_`, and `-`.
- Use dots in role and permission names, not app names.
