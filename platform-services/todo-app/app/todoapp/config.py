"""Application configuration.

Settings are read from environment variables (case-insensitive). This maps
directly onto the values the Helm chart injects, which in turn are fed from the
outputs of the Bicep PostgreSQL module.

Either provide a full ``DATABASE_URL`` or the individual ``DATABASE_*`` parts.
When both are present, ``DATABASE_URL`` wins.
"""

from __future__ import annotations

from functools import lru_cache
from urllib.parse import quote_plus

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    # Application
    app_name: str = Field(default="todo-app")
    log_level: str = Field(default="info")

    # Full connection URL. If set, it takes precedence over the parts below.
    database_url: str | None = Field(default=None)

    # Individual connection parts (map 1:1 to Bicep module outputs).
    database_host: str = Field(default="localhost")
    database_port: int = Field(default=5432)
    database_name: str = Field(default="todos")
    database_user: str = Field(default="postgres")
    database_password: str = Field(default="postgres")
    # Azure Database for PostgreSQL Flexible Server requires SSL by default.
    database_sslmode: str = Field(default="require")

    # Startup DB-connection retry behaviour (the database may not be ready
    # the instant the pod starts).
    db_connect_retries: int = Field(default=15)
    db_connect_backoff_seconds: float = Field(default=2.0)

    def sqlalchemy_url(self) -> str:
        """Build a SQLAlchemy URL from the configured settings."""
        if self.database_url:
            return self.database_url
        user = quote_plus(self.database_user)
        password = quote_plus(self.database_password)
        return (
            f"postgresql+psycopg://{user}:{password}"
            f"@{self.database_host}:{self.database_port}/{self.database_name}"
            f"?sslmode={self.database_sslmode}"
        )


@lru_cache
def get_settings() -> Settings:
    return Settings()
