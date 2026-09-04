FROM python:3.11-slim AS builder

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

WORKDIR /build

COPY --from=ghcr.io/astral-sh/uv:0.11.32 /uv /usr/local/bin/uv

# The build context is this service's own directory. cortex-otel is vendored
# under ./libs/ rather than being a path dependency two levels up in another
# repository, so the service builds standalone.
COPY libs/cortex-otel/ ./libs/cortex-otel/
COPY pyproject.toml uv.lock ./
RUN uv sync --frozen --no-dev --no-install-project --no-editable


FROM python:3.11-slim AS runtime

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONPATH="/app/src" \
    PATH="/app/.venv/bin:$PATH"

WORKDIR /app

# Patch OS packages in the python:*-slim (Debian) base to clear known CVEs
# (e.g. CVE-2026-53615 in util-linux). Runs as root before the USER switch.
RUN apt-get update \
    && apt-get upgrade -y \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /build/.venv ./.venv
COPY src/ ./src/
COPY scripts/ ./scripts/
# config/permissions.yaml is read at runtime by config.permissions._load_yaml
# (resolved relative to src/config/permissions.py three levels up). Without
# this file the loader silently falls back to a hard-coded default set and
# YAML-only permissions disappear in production builds.
COPY config/ ./config/

RUN pip install --no-cache-dir --upgrade "setuptools>=83.0.0" "wheel>=0.46.2"

RUN addgroup --system --gid 1000 appgroup && \
    adduser --system --uid 1000 --ingroup appgroup appuser
USER 1000

EXPOSE 8080

CMD ["python", "-m", "uvicorn", "app.main:build_app", "--factory", "--host", "0.0.0.0", "--port", "8080"]
