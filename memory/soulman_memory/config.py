"""
Configuration for the Soulman Memory Service.

Reads from environment variables with sensible defaults for local Supabase.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path


@dataclass
class MemoryConfig:
    """All configuration for the Memory Service in one place.

    Environment variables override defaults. All values are resolved at init time.
    """

    # ── Postgres connection ──
    pg_host: str = field(
        default_factory=lambda: os.environ.get("SOULMAN_PG_HOST", "localhost")
    )
    pg_port: int = field(
        default_factory=lambda: int(os.environ.get("SOULMAN_PG_PORT", "54322"))
    )
    pg_db: str = field(
        default_factory=lambda: os.environ.get("SOULMAN_PG_DB", "postgres")
    )
    pg_user: str = field(
        default_factory=lambda: os.environ.get("SOULMAN_PG_USER", "postgres")
    )
    pg_password: str = field(
        default_factory=lambda: os.environ.get("SOULMAN_PG_PASSWORD", "postgres")
    )

    # ── Working directory ──
    work_dir: Path = field(
        default_factory=lambda: Path(
            os.environ.get("SOULMAN_WORK_DIR", os.getcwd())
        )
    )

    @property
    def logs_dir(self) -> Path:
        return self.work_dir / "logs"

    @property
    def raw_inputs_log(self) -> Path:
        return self.logs_dir / "raw_inputs.jsonl"

    @property
    def outage_log(self) -> Path:
        return self.logs_dir / "db_outage.jsonl"

    @property
    def pg_dsn(self) -> str:
        return (
            f"host={self.pg_host} port={self.pg_port} "
            f"dbname={self.pg_db} user={self.pg_user} password={self.pg_password}"
        )
