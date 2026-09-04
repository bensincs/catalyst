"""The single tenant this deployment serves.

Upstream, the tenant is resolved per request: the tenant-operator stamps it
into the ext_authz URL path, falling back to the Host subdomain. Neither
applies here. There is no tenant-operator, and this platform publishes apps as
<app>.<subdomain>.<domain> where the subdomain is a fixed routing label
("apps"), not a tenant — so the fallback would silently authorise every request
against a tenant called "apps".

Resolving it from configuration instead means it cannot be influenced by a
request, so a caller cannot reach another tenant's relationships by choosing a
hostname or sending a header.
"""

from __future__ import annotations

import os


def tenant_id_from_env() -> str:
    raw = os.environ.get("AUTHZ_TENANT_ID", "").strip()
    if not raw:
        raise RuntimeError("AUTHZ_TENANT_ID is not set")
    return raw


TENANT_ID = tenant_id_from_env()
