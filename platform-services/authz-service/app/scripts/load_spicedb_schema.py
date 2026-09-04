from __future__ import annotations

import asyncio
import os
import sys
from pathlib import Path

from data.spicedb_client import SpiceDBClient, SpiceDBUnavailableError


def main() -> int:
    schema_path = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("/schema/cortex.zed")
    endpoint = os.environ.get("SPICEDB_ENDPOINT", "localhost:50051")
    token = os.environ.get("SPICEDB_PRESHARED_KEY", "")
    insecure = os.environ.get("SPICEDB_INSECURE", "false").lower() in {
        "1",
        "true",
        "yes",
    }

    if not token:
        print("SPICEDB_PRESHARED_KEY is required", file=sys.stderr)
        return 2
    if not schema_path.exists():
        print(f"Schema file not found: {schema_path}", file=sys.stderr)
        return 2

    client = SpiceDBClient.from_address(endpoint, token=token, insecure=insecure)
    try:
        token = asyncio.run(client.write_schema(schema_path.read_text()))
    except SpiceDBUnavailableError as exc:
        print(f"Schema write failed: {exc}", file=sys.stderr)
        return 1

    print(f"Schema written at {token}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
