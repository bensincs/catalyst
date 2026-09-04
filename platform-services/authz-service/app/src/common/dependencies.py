from __future__ import annotations

import os

from data.spicedb_client import SpiceDBClient

_client: SpiceDBClient | None = None


def get_authz_client() -> SpiceDBClient:
    global _client
    if _client is None:
        endpoint = os.environ.get("SPICEDB_ENDPOINT", "localhost:50051")
        token = os.environ.get("SPICEDB_PRESHARED_KEY", "")
        insecure = os.environ.get("SPICEDB_INSECURE", "false").lower() == "true"
        _client = SpiceDBClient.from_address(endpoint, token=token, insecure=insecure)
    return _client


def reset_authz_client() -> None:
    global _client
    _client = None
