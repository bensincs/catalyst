"""Test fixtures.

The application reads its database configuration at import time, so the test
database URL must be set in the environment *before* ``todoapp`` is imported.
A temporary on-disk SQLite database is used to keep tests fast and hermetic.
"""

from __future__ import annotations

import os
import tempfile
from pathlib import Path

import pytest

_TMPDIR = tempfile.mkdtemp(prefix="todoapp-test-")
_DB_PATH = Path(_TMPDIR) / "test.db"
os.environ["DATABASE_URL"] = f"sqlite+pysqlite:///{_DB_PATH}"

from fastapi.testclient import TestClient  # noqa: E402
from todoapp.db import Base, engine  # noqa: E402
from todoapp.main import app  # noqa: E402


@pytest.fixture(autouse=True)
def _fresh_schema():
    """Give every test an empty schema."""
    Base.metadata.drop_all(bind=engine)
    Base.metadata.create_all(bind=engine)
    yield


@pytest.fixture()
def client():
    with TestClient(app) as test_client:
        yield test_client
