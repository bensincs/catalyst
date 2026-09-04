from __future__ import annotations

import asyncio
import os
import sys

from data.spicedb_client import SpiceDBClient, SpiceDBUnavailableError


def main() -> int:
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

    client = SpiceDBClient.from_address(endpoint, token=token, insecure=insecure)
    try:
        asyncio.run(client.wait_until_ready())
    except SpiceDBUnavailableError as exc:
        print(f"SpiceDB readiness check failed: {exc}", file=sys.stderr)
        return 1

    print(f"SpiceDB gRPC ready at {endpoint}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
