from __future__ import annotations

import pathlib
from dataclasses import dataclass

import yaml


@dataclass(frozen=True)
class Permission:
    key: str
    description: str
    category: str
    namespace: str = "platform"

    @property
    def resource_id(self) -> str:
        return self.key.replace(".", "_")


def _load_permissions() -> list[Permission]:
    config_path = pathlib.Path(__file__).parent.parent.parent / "config" / "permissions.yaml"

    if not config_path.exists():
        return _get_default_permissions()

    try:
        with open(config_path) as f:
            data = yaml.safe_load(f)

        permissions = []
        for perm_data in data.get("platform_permissions", []):
            permissions.append(
                Permission(
                    key=perm_data["key"],
                    description=perm_data["description"],
                    category="administration",
                    namespace=perm_data.get("namespace", "platform"),
                )
            )
        return permissions
    except Exception:
        return _get_default_permissions()


def _get_default_permissions() -> list[Permission]:
    return [
        Permission(
            key="users.manage",
            description="Create, update, and delete user accounts",
            category="administration",
            namespace="platform",
        ),
        Permission(
            key="roles.manage",
            description="Create, modify, and delete roles",
            category="administration",
            namespace="platform",
        ),
        Permission(
            key="permissions.manage",
            description="Grant and revoke permissions",
            category="administration",
            namespace="platform",
        ),
        Permission(
            key="tenants.manage",
            description="Create and configure tenants",
            category="administration",
            namespace="platform",
        ),
        Permission(
            key="audit.view",
            description="Access audit trail and security logs",
            category="administration",
            namespace="platform",
        ),
        Permission(
            key="settings.manage",
            description="Configure platform-wide settings",
            category="administration",
            namespace="platform",
        ),
    ]


PERMISSIONS = _load_permissions()
PERMISSION_BY_KEY = {p.key: p for p in PERMISSIONS}

_DYNAMIC_PERMISSIONS: dict[str, Permission] = {}


def get_permission(key: str) -> Permission | None:
    return PERMISSION_BY_KEY.get(key) or _DYNAMIC_PERMISSIONS.get(key)


def register_permission(permission: Permission) -> None:
    _DYNAMIC_PERMISSIONS[permission.key] = permission


def list_permissions(namespace: str | None = None) -> list[Permission]:
    all_perms = list(PERMISSIONS) + list(_DYNAMIC_PERMISSIONS.values())
    if namespace is None:
        return all_perms
    return [p for p in all_perms if p.namespace == namespace]
