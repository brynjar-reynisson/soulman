"""
Soulman Memory MCP Server — thin protocol adapter for the Memory Service.

This is NOT the storage engine. It is an MCP stdio wrapper that delegates
all operations to `soulman_memory.MemoryStore`. The MemoryStore owns the
connection pool, SQL, filesystem mirroring, and replay logic.

Ownership:
    soulman_memory/          ← Memory module (owns storage)
    soulman-memory-mcp-server.py  ← MCP adapter (protocol only)

Exposes three MCP tools:
    soulman_store    — INSERT/UPDATE/UPSERT + filesystem mirror
    soulman_retrieve — SELECT, list_tables, semantic search, read_log
    soulman_replay   — Replay deferred writes from db_outage.jsonl
"""

from __future__ import annotations

import json
import logging
import sys
from pathlib import Path
from typing import Any

from mcp.server.fastmcp import FastMCP

# Ensure the parent directory is on sys.path so we can import soulman_memory
# regardless of which working directory the MCP server is launched from.
_SERVER_DIR = Path(__file__).resolve().parent
if str(_SERVER_DIR) not in sys.path:
    sys.path.insert(0, str(_SERVER_DIR))

from soulman_memory import MemoryConfig, MemoryStore

# ── Logging ──────────────────────────────────────────────────────────────────

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("soulman.memory.mcp")

# ── Service instance ─────────────────────────────────────────────────────────

_config = MemoryConfig()
_store = MemoryStore(_config)
_store.start()

# ── MCP server ───────────────────────────────────────────────────────────────

mcp = FastMCP("soulman-memory", version="0.2.0")


# ── Tools ────────────────────────────────────────────────────────────────────


@mcp.tool()
def soulman_store(
    table: str,
    data: str,
    schema: str = "memory_dev",
    operation: str = "insert",
    conflict_col: str = "id",
) -> str:
    """Store a row in Postgres, with filesystem mirror for raw_inputs.

    Delegates to MemoryStore.store().

    Args:
        table: Target table name (raw_inputs, episodes, facts, procedures, goals, action_log)
        data: JSON string of column:value pairs
        schema: Postgres schema (memory_dev or memory_prod)
        operation: 'insert', 'upsert', or 'update'
        conflict_col: Column for conflict resolution (default: id)

    Returns:
        JSON with status, backend(s), generated IDs.
    """
    try:
        row = json.loads(data)
    except json.JSONDecodeError as e:
        return json.dumps({"status": "error", "error": f"Invalid JSON data: {e}"})

    result = _store.store(table, row, schema, operation, conflict_col)
    return json.dumps(result, default=str)


@mcp.tool()
def soulman_retrieve(
    query_type: str = "select",
    table: str = "",
    schema: str = "memory_dev",
    columns: str = "*",
    where: str = "",
    order_by: str = "",
    limit: int = 50,
    embedding: str = "",
) -> str:
    """Read data from Postgres or filesystem logs.

    Delegates to MemoryStore.select(), list_tables(), semantic_search(), or read_log().

    Args:
        query_type: 'select', 'list_tables', 'read_log', or 'semantic'
        table: Target table (for select/semantic/read_log)
        schema: Postgres schema (memory_dev or memory_prod)
        columns: Columns to select (default: *)
        where: WHERE clause (for select/semantic)
        order_by: ORDER BY clause (for select)
        limit: Row limit (default: 50, max: 500)
        embedding: JSON array of 1536 floats for semantic search

    Returns:
        JSON with rows, count, and source backend.
    """
    limit = min(limit, 500)

    if query_type == "read_log":
        result = _store.read_log(table or "raw_inputs", limit)
    elif query_type == "list_tables":
        result = _store.list_tables(schema)
    elif query_type == "semantic":
        if not embedding:
            return json.dumps(
                {"status": "error", "error": "embedding required for semantic search"}
            )
        try:
            emb = json.loads(embedding)
        except (json.JSONDecodeError, ValueError) as e:
            return json.dumps({"status": "error", "error": f"Invalid embedding: {e}"})
        result = _store.semantic_search(table, emb, schema, columns, where, limit)
    elif query_type == "select":
        if not table:
            return json.dumps(
                {"status": "error", "error": "table required for select"}
            )
        result = _store.select(table, schema, columns, where, order_by, limit)
    else:
        return json.dumps({"status": "error", "error": f"Unknown query_type: {query_type}"})

    return json.dumps(result, default=str)


@mcp.tool()
def soulman_replay(schema: str = "memory_dev") -> str:
    """Replay deferred writes from logs/db_outage.jsonl into Postgres.

    Delegates to MemoryStore.replay().

    Args:
        schema: Postgres schema (memory_dev or memory_prod)

    Returns:
        JSON with replayed count, failed count, and details.
    """
    result = _store.replay(schema)
    return json.dumps(result, default=str)


@mcp.tool()
def soulman_health() -> str:
    """Check if the memory service is healthy (Postgres reachable).

    Returns:
        JSON with healthy (bool), config summary.
    """
    return json.dumps({
        "healthy": _store.healthy,
        "pg_host": _config.pg_host,
        "pg_port": _config.pg_port,
        "pg_db": _config.pg_db,
        "work_dir": str(_config.work_dir),
    })


# ── Entry Point ──────────────────────────────────────────────────────────────

def main():
    """Run the Soulman Memory MCP server via stdio."""
    logger.info(
        "Soulman Memory MCP server starting — PG %s:%s/%s, work_dir %s",
        _config.pg_host,
        _config.pg_port,
        _config.pg_db,
        _config.work_dir,
    )
    try:
        mcp.run(transport="stdio")
    finally:
        _store.stop()


if __name__ == "__main__":
    main()
