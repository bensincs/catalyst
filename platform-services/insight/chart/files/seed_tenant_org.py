#!/usr/bin/env python3
"""Idempotent tenant bootstrap: ensure a default organization exists.

A freshly provisioned tenant database has schema (via migrations) but no
``organizations`` row, so meeting groups cannot be created or listed (the
frontend "Create your first meeting group" empty state needs an organization to
attach to). This seed creates exactly one organization for the tenant, plus the
system user it must reference.

It is environment-agnostic (unlike the backend image's dev-only seed_data.py)
and safe to run on every deploy: every write uses ON CONFLICT DO NOTHING, so
re-running is a no-op and it never overwrites operator/user changes.

This file is chart-owned (services/insight/chart/files): it is inlined into a
ConfigMap and mounted into the backend `seed-org` init container, so it does not
depend on the backend image shipping it. It uses only sqlalchemy + asyncpg +
raw SQL (both present in the backend image's venv) and nothing from the insight
codebase.

organizations.admin_id / created_by / updated_by are NOT NULL and have foreign
keys to users.id, so a bare placeholder UUID would violate the FK. We therefore
seed a fixed, well-known system user first and point the organization at it. The
real org admin is assigned later (first-login onboarding), which may repoint
admin_id; this seed never touches an organization row that already exists.

Configuration (all optional, sensible defaults):
    DATABASE_URL            required; async SQLAlchemy URL.
    SEED_ORGANIZATION_ID    organization id to ensure. Must match the id the BFF
                            queries (BFF ORGANIZATION_ID). Default: the all-zeros
                            system org.
    SEED_ORGANIZATION_NAME  default: "Default Organization".
    SEED_ORGANIZATION_SLUG  default: "default".
    SEED_SYSTEM_USER_ID     fixed system user id used for the org FKs.
    SEED_SYSTEM_USER_EMAIL  default: "system@insight.local".
"""

from __future__ import annotations

import asyncio
import logging
import os
from typing import Final

from sqlalchemy import text
from sqlalchemy.ext.asyncio import create_async_engine

logger = logging.getLogger(__name__)

SCHEMA: Final[str] = "insight-saas"

DEFAULT_ORGANIZATION_ID: Final[str] = "00000000-0000-0000-0000-000000000000"
DEFAULT_SYSTEM_USER_ID: Final[str] = "00000000-0000-0000-0000-000000000001"


async def _table_exists(conn, table_name: str) -> bool:
    result = await conn.scalar(
        text(
            """
            SELECT EXISTS (
              SELECT 1 FROM information_schema.tables
              WHERE table_schema = :schema AND table_name = :table
            )
            """
        ),
        {"schema": SCHEMA, "table": table_name},
    )
    return bool(result)


async def _seed_system_user(conn, user_id: str, email: str) -> None:
    await conn.execute(
        text(
            f"""
            INSERT INTO "{SCHEMA}".users (id, email, name, role, is_active)
            VALUES (:id, :email, :name, 'system', TRUE)
            ON CONFLICT (id) DO NOTHING
            """
        ),
        {"id": user_id, "email": email, "name": "System"},
    )


async def _seed_organization(
    conn, org_id: str, name: str, slug: str, admin_id: str
) -> None:
    await conn.execute(
        text(
            f"""
            INSERT INTO "{SCHEMA}".organizations (
                id, name, slug, admin_id, status, created_by, updated_by
            ) VALUES (
                :id, :name, :slug, :admin_id, 'active', :admin_id, :admin_id
            )
            ON CONFLICT (id) DO NOTHING
            """
        ),
        {"id": org_id, "name": name, "slug": slug, "admin_id": admin_id},
    )


async def seed() -> int:
    database_url = os.getenv("DATABASE_URL")
    if not database_url:
        logger.error("DATABASE_URL is required for tenant org seed")
        return 1

    org_id = os.getenv("SEED_ORGANIZATION_ID", DEFAULT_ORGANIZATION_ID)
    org_name = os.getenv("SEED_ORGANIZATION_NAME", "Default Organization")
    org_slug = os.getenv("SEED_ORGANIZATION_SLUG", "default")
    system_user_id = os.getenv("SEED_SYSTEM_USER_ID", DEFAULT_SYSTEM_USER_ID)
    system_user_email = os.getenv("SEED_SYSTEM_USER_EMAIL", "system@insight.local")

    engine = create_async_engine(database_url, future=True)
    try:
        async with engine.begin() as conn:
            if not await _table_exists(conn, "organizations") or not await _table_exists(
                conn, "users"
            ):
                logger.warning(
                    "organizations/users tables not found; skipping tenant org seed"
                )
                return 0
            await _seed_system_user(conn, system_user_id, system_user_email)
            await _seed_organization(conn, org_id, org_name, org_slug, system_user_id)
        logger.info(
            "tenant org seed complete: organization %s ensured (admin %s)",
            org_id,
            system_user_id,
        )
        return 0
    finally:
        await engine.dispose()


def main() -> int:
    logging.basicConfig(level=logging.INFO, format="%(levelname)s %(name)s %(message)s")
    return asyncio.run(seed())


if __name__ == "__main__":
    raise SystemExit(main())
