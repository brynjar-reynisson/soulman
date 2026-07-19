"""
Soulman Memory — the storage engine for Project Soulman.

This package owns all storage logic: Postgres connection pooling, SQL,
filesystem log mirroring, and replay. It is a plain Python library with no
MCP dependency — protocol adapters (MCP, HTTP, CLI) import and delegate here.

Public API:
    from soulman_memory import MemoryStore, MemoryConfig

    config = MemoryConfig()
    store = MemoryStore(config)
    store.start()

    # Write
    store.insert("raw_inputs", {"stimulus_id": "...", ...}, schema="memory_dev")
    store.upsert("facts", {...}, schema="memory_dev")

    # Read
    store.select("episodes", where="source = 'human'", schema="memory_dev")
    store.semantic_search("facts", embedding=[0.1, 0.2, ...], schema="memory_dev")
    store.read_log("raw_inputs", limit=20)

    # Replay
    store.replay(schema="memory_dev")

    store.stop()
"""

from .config import MemoryConfig
from .store import MemoryStore

__all__ = ["MemoryStore", "MemoryConfig"]
