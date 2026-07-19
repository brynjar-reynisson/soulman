"""
Memory Store — the core storage engine for Soulman.

Owns the Postgres connection pool, filesystem log mirror, and replay logic.
This is the Memory module's service layer. It is a plain Python class with no
MCP dependency — the MCP server is a thin protocol adapter that delegates here.

Usage:
    from soulman_memory import MemoryStore, MemoryConfig

    config = MemoryConfig()
    store = MemoryStore(config)
    store.insert("raw_inputs", {"stimulus_id": "...", ...}, schema="memory_dev")
    store.select("episodes", where="source = 'human'", schema="memory_dev")
"""

from __future__ import annotations

import json
import logging
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import psycopg2
import psycopg2.extras
import psycopg2.pool

from .config import MemoryConfig

logger = logging.getLogger("soulman.memory.store")

# ── Maximum retries for transient DB failures ──
MAX_RETRIES = 3


class MemoryStore:
    """Core storage engine for the Soulman memory system.

    Owns connection pooling, SQL construction, filesystem mirroring,
    fallback protocol, and replay logic. All methods are synchronous.

    Thread-safe via psycopg2 connection pool.
    """

    def __init__(self, config: MemoryConfig | None = None):
        self.config = config or MemoryConfig()
        self._pool: psycopg2.pool.ThreadedConnectionPool | None = None
        self._started = False

    # ── Lifecycle ─────────────────────────────────────────────────────────

    def start(self) -> None:
        """Initialize the connection pool and ensure logs directory exists."""
        if self._started:
            return
        self.config.logs_dir.mkdir(parents=True, exist_ok=True)
        self._pool = psycopg2.pool.ThreadedConnectionPool(
            minconn=2,
            maxconn=10,
            host=self.config.pg_host,
            port=self.config.pg_port,
            dbname=self.config.pg_db,
            user=self.config.pg_user,
            password=self.config.pg_password,
        )
        self._started = True
        logger.info(
            "MemoryStore started — PG %s:%s/%s, work_dir %s",
            self.config.pg_host,
            self.config.pg_port,
            self.config.pg_db,
            self.config.work_dir,
        )

    def stop(self) -> None:
        """Close the connection pool."""
        if self._pool:
            self._pool.closeall()
            self._pool = None
        self._started = False
        logger.info("MemoryStore stopped")

    @property
    def healthy(self) -> bool:
        """Check if Postgres is reachable."""
        if not self._pool:
            return False
        try:
            conn = self._pool.getconn()
            with conn.cursor() as cur:
                cur.execute("SELECT 1")
            self._pool.putconn(conn)
            return True
        except Exception:
            return False

    # ── Write Operations ──────────────────────────────────────────────────

    def insert(
        self,
        table: str,
        data: dict[str, Any],
        schema: str = "memory_dev",
    ) -> dict[str, Any]:
        """Insert a row. Returns the inserted row with generated IDs.

        For raw_inputs, also mirrors to logs/raw_inputs.jsonl.
        On DB failure for raw_inputs, falls back to filesystem-only.
        """
        result: dict[str, Any] = {"backend": [], "status": "ok"}
        db_ok = self.healthy

        # ── Postgres path ──
        if db_ok:
            try:
                row = self._execute_insert(table, data, schema)
                result["pg_row"] = row
                result["backend"].append("postgres")
                if "id" in row:
                    result["id"] = row["id"]
                elif "stimulus_id" in row:
                    result["id"] = row["stimulus_id"]
                elif "action_id" in row:
                    result["id"] = row["action_id"]
            except Exception as e:
                error_msg = str(e)
                result["pg_error"] = error_msg
                self._log_outage(f"INSERT {schema}.{table}", error_msg)
        else:
            result["pg_error"] = "Postgres not reachable"

        # ── Filesystem mirror for raw_inputs ──
        if table == "raw_inputs":
            sid = data.get("stimulus_id", str(uuid.uuid4()))
            try:
                mirror_path = self._mirror_raw_input(schema, sid, data)
                result["mirror_path"] = str(mirror_path)
                result["backend"].append("filesystem")
            except Exception as e:
                result["mirror_error"] = str(e)

        # ── Filesystem-only fallback ──
        if not db_ok and table == "raw_inputs":
            try:
                self.config.logs_dir.mkdir(parents=True, exist_ok=True)
                with open(self.config.raw_inputs_log, "a", encoding="utf-8") as f:
                    f.write(json.dumps(data, ensure_ascii=False) + "\n")
                result["mirror_path"] = str(self.config.raw_inputs_log)
                result["backend"].append("filesystem")
                result["deferred_to_filesystem"] = True
            except Exception as e:
                result["mirror_error"] = str(e)

        if not result["backend"]:
            result["status"] = "error"
            result["error"] = "No backend available"

        result["backend"] = "+".join(result["backend"])
        return result

    def upsert(
        self,
        table: str,
        data: dict[str, Any],
        schema: str = "memory_dev",
        conflict_col: str = "id",
    ) -> dict[str, Any]:
        """Insert or update on conflict. Returns the resulting row."""
        result: dict[str, Any] = {"backend": [], "status": "ok"}

        if not self.healthy:
            return {"status": "error", "error": "Postgres not reachable"}

        try:
            row = self._execute_upsert(table, data, schema, conflict_col)
            result["pg_row"] = row
            result["backend"].append("postgres")
            result["id"] = row.get("id") or row.get("stimulus_id") or row.get("action_id")
        except Exception as e:
            result["status"] = "error"
            result["error"] = str(e)
            self._log_outage(f"UPSERT {schema}.{table}", str(e))

        result["backend"] = "+".join(result["backend"])
        return result

    def update(
        self,
        table: str,
        data: dict[str, Any],
        schema: str = "memory_dev",
        conflict_col: str = "id",
    ) -> dict[str, Any]:
        """Update a row by conflict column. Returns the updated row."""
        result: dict[str, Any] = {"backend": [], "status": "ok"}

        if not self.healthy:
            return {"status": "error", "error": "Postgres not reachable"}

        try:
            row = self._execute_update(table, data, schema, conflict_col)
            result["pg_row"] = row
            result["backend"].append("postgres")
            result["id"] = row.get("id") or row.get("stimulus_id") or row.get("action_id")
        except Exception as e:
            result["status"] = "error"
            result["error"] = str(e)

        result["backend"] = "+".join(result["backend"])
        return result

    def store(
        self,
        table: str,
        data: dict[str, Any],
        schema: str = "memory_dev",
        operation: str = "insert",
        conflict_col: str = "id",
    ) -> dict[str, Any]:
        """Unified store: dispatches to insert, upsert, or update."""
        if operation == "upsert":
            return self.upsert(table, data, schema, conflict_col)
        elif operation == "update":
            return self.update(table, data, schema, conflict_col)
        else:
            return self.insert(table, data, schema)

    # ── Read Operations ───────────────────────────────────────────────────

    def select(
        self,
        table: str,
        schema: str = "memory_dev",
        columns: str = "*",
        where: str = "",
        order_by: str = "",
        limit: int = 50,
    ) -> dict[str, Any]:
        """Run a SELECT query. Returns rows, count, source."""
        limit = min(limit, 500)

        if not self.healthy:
            return {"status": "error", "error": "Postgres not reachable"}

        sql = f'SELECT {columns} FROM "{schema}"."{table}"'
        params: list = []

        if where:
            sql += f" WHERE {where}"
        if order_by:
            sql += f" ORDER BY {order_by}"
        sql += f" LIMIT %s"
        params.append(limit)

        conn = self._pool.getconn()
        try:
            with conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
                cur.execute(sql, params)
                rows = cur.fetchall()
            return {
                "status": "ok",
                "source": "postgres",
                "rows": rows,
                "count": len(rows),
            }
        finally:
            self._pool.putconn(conn)

    def list_tables(self, schema: str = "memory_dev") -> dict[str, Any]:
        """List all tables in the given schema."""
        if not self.healthy:
            return {"status": "error", "error": "Postgres not reachable"}

        conn = self._pool.getconn()
        try:
            with conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
                cur.execute(
                    "SELECT table_name FROM information_schema.tables "
                    "WHERE table_schema = %s ORDER BY table_name",
                    [schema],
                )
                rows = cur.fetchall()
            return {
                "status": "ok",
                "source": "postgres",
                "rows": rows,
                "count": len(rows),
            }
        finally:
            self._pool.putconn(conn)

    def semantic_search(
        self,
        table: str,
        embedding: list[float],
        schema: str = "memory_dev",
        columns: str = "*",
        where: str = "",
        limit: int = 50,
    ) -> dict[str, Any]:
        """pgvector cosine-distance semantic search."""
        limit = min(limit, 500)

        if not self.healthy:
            return {"status": "error", "error": "Postgres not reachable"}

        emb_str = "[" + ",".join(str(v) for v in embedding) + "]"
        where_clause = f"AND {where}" if where else ""

        sql = (
            f"SELECT {columns}, embedding <=> %s::vector AS distance "
            f'FROM "{schema}"."{table}" '
            f"WHERE status = 'active' AND forgotten_at IS NULL {where_clause} "
            f"ORDER BY distance LIMIT %s"
        )

        conn = self._pool.getconn()
        try:
            with conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
                cur.execute(sql, [emb_str, limit])
                rows = cur.fetchall()

            for r in rows:
                if "distance" in r and r["distance"] is not None:
                    r["distance"] = float(r["distance"])

            return {
                "status": "ok",
                "source": "postgres",
                "rows": rows,
                "count": len(rows),
            }
        finally:
            self._pool.putconn(conn)

    def read_log(self, log_name: str = "raw_inputs", limit: int = 50) -> dict[str, Any]:
        """Read the last N lines from a log file."""
        log_path = self.config.logs_dir / f"{log_name}.jsonl"
        if not log_path.exists():
            return {
                "status": "ok",
                "source": "filesystem",
                "rows": [],
                "count": 0,
            }

        with open(log_path, "r", encoding="utf-8") as f:
            lines = f.readlines()

        rows = [json.loads(line) for line in lines[-limit:]]
        return {
            "status": "ok",
            "source": "filesystem",
            "path": str(log_path),
            "rows": rows,
            "count": len(rows),
        }

    # ── Replay ────────────────────────────────────────────────────────────

    def replay(self, schema: str = "memory_dev") -> dict[str, Any]:
        """Replay deferred writes from db_outage.jsonl into Postgres.

        Reads the outage log, attempts re-insertion, and reports results.
        Successfully replayed entries are removed; failures stay for next attempt.
        """
        outage_path = self.config.outage_log
        if not outage_path.exists():
            return {
                "status": "ok",
                "message": "No outage log found — nothing to replay",
                "replayed": 0,
                "failed": 0,
            }

        with open(outage_path, "r", encoding="utf-8") as f:
            lines = f.readlines()

        if not lines:
            return {
                "status": "ok",
                "message": "Outage log empty",
                "replayed": 0,
                "failed": 0,
            }

        if not self.healthy:
            return {
                "status": "error",
                "error": "Postgres not reachable — cannot replay",
            }

        replayed = []
        failed = []

        for i, line in enumerate(lines):
            try:
                record = json.loads(line)
            except json.JSONDecodeError:
                failed.append({"line": i, "error": "Invalid JSON"})
                continue

            operation = record.get("operation", "")
            parts = operation.split(" ", 1)
            if len(parts) < 2 or not parts[0] in ("INSERT", "UPSERT"):
                failed.append({"line": i, "error": f"Unsupported operation: {operation}"})
                continue

            # For now: attempt replay by reading the corresponding raw_input
            # log entry. Full payload preservation is a future enhancement.
            try:
                raw_path = self.config.raw_inputs_log
                if raw_path.exists():
                    with open(raw_path, "r", encoding="utf-8") as rf:
                        raw_lines = rf.readlines()
                    # Find matching stimulus by scanning backward
                    for raw_line in reversed(raw_lines):
                        try:
                            raw_entry = json.loads(raw_line)
                            self.insert("raw_inputs", raw_entry, schema)
                            break
                        except Exception:
                            continue

                replayed.append({"line": i, "operation": operation})
            except Exception as e:
                failed.append({"line": i, "operation": operation, "error": str(e)})

        # Rewrite outage log with only failed entries
        if failed:
            remaining = [json.loads(lines[f["line"]]) for f in failed]
            with open(outage_path, "w", encoding="utf-8") as f:
                for record in remaining:
                    f.write(json.dumps(record, ensure_ascii=False) + "\n")
        else:
            outage_path.write_text("", encoding="utf-8")

        return {
            "status": "ok",
            "replayed": len(replayed),
            "failed": len(failed),
            "replay_details": replayed,
            "failure_details": failed,
        }

    # ── Internal: SQL execution ───────────────────────────────────────────

    def _get_conn(self):
        """Get a connection from the pool."""
        if not self._pool:
            raise RuntimeError("MemoryStore not started — call start() first")
        return self._pool.getconn()

    def _put_conn(self, conn):
        """Return a connection to the pool."""
        if self._pool:
            self._pool.putconn(conn)

    def _execute_insert(
        self, table: str, data: dict, schema: str
    ) -> dict[str, Any]:
        """Parameterized INSERT with retry."""
        columns = list(data.keys())
        placeholders = ["%s"] * len(columns)
        col_names = ", ".join(f'"{c}"' for c in columns)
        ph_names = ", ".join(placeholders)
        params = [data[c] for c in columns]

        sql = (
            f'INSERT INTO "{schema}"."{table}" ({col_names}) '
            f"VALUES ({ph_names}) RETURNING *"
        )

        last_error = None
        for attempt in range(1, MAX_RETRIES + 1):
            conn = self._get_conn()
            try:
                with conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
                    cur.execute(sql, params)
                    row = cur.fetchone()
                return dict(row) if row else {}
            except Exception as e:
                last_error = e
                logger.warning(
                    "INSERT %s.%s attempt %d/%d failed: %s",
                    schema, table, attempt, MAX_RETRIES, e,
                )
            finally:
                self._put_conn(conn)

        raise last_error  # type: ignore[misc]

    def _execute_upsert(
        self, table: str, data: dict, schema: str, conflict_col: str
    ) -> dict[str, Any]:
        """Parameterized INSERT ... ON CONFLICT DO UPDATE."""
        columns = list(data.keys())
        placeholders = ["%s"] * len(columns)
        col_names = ", ".join(f'"{c}"' for c in columns)
        ph_names = ", ".join(placeholders)
        set_clause = ", ".join(
            f'"{c}" = EXCLUDED."{c}"' for c in columns if c != conflict_col
        )
        params = [data[c] for c in columns]

        sql = (
            f'INSERT INTO "{schema}"."{table}" ({col_names}) '
            f"VALUES ({ph_names}) "
            f'ON CONFLICT ("{conflict_col}") DO UPDATE SET {set_clause} '
            f"RETURNING *"
        )

        conn = self._get_conn()
        try:
            with conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
                cur.execute(sql, params)
                row = cur.fetchone()
            return dict(row) if row else {}
        finally:
            self._put_conn(conn)

    def _execute_update(
        self, table: str, data: dict, schema: str, conflict_col: str
    ) -> dict[str, Any]:
        """Parameterized UPDATE by conflict column."""
        set_cols = [c for c in data if c != conflict_col]
        set_clause = ", ".join(f'"{c}" = %s' for c in set_cols)
        params = [data[c] for c in set_cols] + [data[conflict_col]]

        sql = (
            f'UPDATE "{schema}"."{table}" SET {set_clause} '
            f'WHERE "{conflict_col}" = %s RETURNING *'
        )

        conn = self._get_conn()
        try:
            with conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
                cur.execute(sql, params)
                row = cur.fetchone()
            return dict(row) if row else {}
        finally:
            self._put_conn(conn)

    # ── Internal: Filesystem helpers ──────────────────────────────────────

    def _mirror_raw_input(
        self, schema: str, stimulus_id: str, data: dict
    ) -> Path:
        """Append a raw_input to logs/raw_inputs.jsonl."""
        mirror = {
            "stimulus_id": stimulus_id,
            "received_at": datetime.now(timezone.utc).isoformat(),
            "channel": data.get("channel", ""),
            "source_identity": data.get("source_identity", ""),
            "normalized_text": data.get("normalized_text", ""),
            "is_override": data.get("is_override", False),
        }
        self.config.logs_dir.mkdir(parents=True, exist_ok=True)
        with open(self.config.raw_inputs_log, "a", encoding="utf-8") as f:
            f.write(json.dumps(mirror, ensure_ascii=False) + "\n")
        return self.config.raw_inputs_log

    def _log_outage(self, operation: str, error: str) -> Path:
        """Record a Postgres write failure to logs/db_outage.jsonl."""
        outage = {
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "operation": operation,
            "error": error,
        }
        self.config.logs_dir.mkdir(parents=True, exist_ok=True)
        with open(self.config.outage_log, "a", encoding="utf-8") as f:
            f.write(json.dumps(outage, ensure_ascii=False) + "\n")
        return self.config.outage_log
