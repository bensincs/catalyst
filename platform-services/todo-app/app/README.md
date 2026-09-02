# todoapp

Minimal FastAPI + PostgreSQL todo service with an embedded single-page UI.

## Endpoints

| Method | Path              | Description                     |
| ------ | ----------------- | ------------------------------- |
| GET    | `/`               | Embedded UI                     |
| GET    | `/docs`           | OpenAPI / Swagger UI            |
| GET    | `/healthz`        | Liveness (no DB access)         |
| GET    | `/readyz`         | Readiness (checks DB)           |
| GET    | `/api/todos`      | List todos                      |
| POST   | `/api/todos`      | Create a todo                   |
| GET    | `/api/todos/{id}` | Fetch one                       |
| PATCH  | `/api/todos/{id}` | Update title / completed        |
| DELETE | `/api/todos/{id}` | Delete                          |

## Configuration

All configuration is via environment variables. Provide either a full
`DATABASE_URL`, or the individual parts (which map 1:1 onto the Bicep module
outputs consumed by the Helm chart):

| Variable            | Default     | Notes                                   |
| ------------------- | ----------- | --------------------------------------- |
| `DATABASE_URL`      | _(unset)_   | Full SQLAlchemy URL; wins when set      |
| `DATABASE_HOST`     | `localhost` | Postgres FQDN                           |
| `DATABASE_PORT`     | `5432`      |                                         |
| `DATABASE_NAME`     | `todos`     |                                         |
| `DATABASE_USER`     | `postgres`  |                                         |
| `DATABASE_PASSWORD` | `postgres`  | Injected from a Kubernetes Secret       |
| `DATABASE_SSLMODE`  | `require`   | Azure Flexible Server requires SSL      |

## Local development

```bash
uv sync
uv run uvicorn todoapp.main:app --reload
```

## Tests

```bash
uv sync
uv run pytest
```

## Container

```bash
docker build -t todoapp:dev .
docker run --rm -p 8000:8000 \
  -e DATABASE_HOST=host.docker.internal \
  -e DATABASE_SSLMODE=prefer \
  todoapp:dev
```
