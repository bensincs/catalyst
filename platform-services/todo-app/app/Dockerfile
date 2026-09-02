# syntax=docker/dockerfile:1

ARG PYTHON_VERSION=3.12

# --------------------------------------------------------------------------- #
# builder: resolve + install dependencies into an isolated virtualenv
# --------------------------------------------------------------------------- #
FROM ghcr.io/astral-sh/uv:python${PYTHON_VERSION}-bookworm-slim AS builder

ENV UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy \
    UV_PYTHON_DOWNLOADS=0

WORKDIR /app

# Copy only dependency metadata first for better layer caching.
COPY pyproject.toml uv.lock ./
# No BuildKit cache mount here on purpose. It saves a little time locally but
# makes the image buildable ONLY under BuildKit — and the platform builds these
# in ACR Tasks, which uses a classic builder and fails with "the --mount option
# requires BuildKit". Layer caching on the two files copied above already gives
# most of the benefit.
RUN uv sync --frozen --no-install-project --no-dev

# --------------------------------------------------------------------------- #
# runtime: slim image containing the venv + application source only
# --------------------------------------------------------------------------- #
FROM python:${PYTHON_VERSION}-slim-bookworm AS runtime

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONPATH=/app \
    PATH="/app/.venv/bin:${PATH}"

# Unprivileged runtime user with a fixed UID/GID (matches the Helm chart's
# securityContext.runAsUser / runAsGroup so runAsNonRoot works cleanly).
RUN groupadd --gid 10001 app \
    && useradd --uid 10001 --gid 10001 --no-create-home --home-dir /app --shell /usr/sbin/nologin app

WORKDIR /app

COPY --from=builder --chown=app:app /app/.venv /app/.venv
COPY --chown=app:app todoapp ./todoapp

USER app

EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=3s --start-period=20s --retries=3 \
    CMD python -c "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:8000/healthz', timeout=2).status == 200 else 1)"

CMD ["uvicorn", "todoapp.main:app", "--host", "0.0.0.0", "--port", "8000"]
