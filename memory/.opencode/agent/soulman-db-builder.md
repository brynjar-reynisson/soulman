## Schema Name Resolution

You are invoked from `~/soulman-prod/memory/` — use schema `memory_prod`.

The plan uses `memory` as a placeholder. Substitute it with `memory_prod`. All table references (`memory.raw_inputs`, `memory.episodes`, etc.) should use `memory_prod`.