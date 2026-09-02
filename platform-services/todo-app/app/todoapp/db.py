"""Database engine, session factory and declarative base."""

from __future__ import annotations

from collections.abc import Iterator

from sqlalchemy import create_engine
from sqlalchemy.engine import Engine
from sqlalchemy.orm import DeclarativeBase, Session, sessionmaker

from .config import get_settings


class Base(DeclarativeBase):
    """Declarative base for all ORM models."""


def _make_engine() -> Engine:
    settings = get_settings()
    url = settings.sqlalchemy_url()

    connect_args: dict[str, object] = {}
    kwargs: dict[str, object] = {"pool_pre_ping": True, "future": True}

    # SQLite is only used for local development / tests.
    if url.startswith("sqlite"):
        connect_args["check_same_thread"] = False
        if ":memory:" in url:
            from sqlalchemy.pool import StaticPool

            kwargs["poolclass"] = StaticPool

    return create_engine(url, connect_args=connect_args, **kwargs)


engine: Engine = _make_engine()
SessionLocal = sessionmaker(
    bind=engine, autoflush=False, expire_on_commit=False, class_=Session
)


def get_session() -> Iterator[Session]:
    """FastAPI dependency that yields a scoped database session."""
    with SessionLocal() as session:
        yield session
