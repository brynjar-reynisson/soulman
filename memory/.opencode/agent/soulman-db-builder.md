## Schema Name Resolution

You are invoked from one of two working directories:
- `~/soulman-dev/memory/` → use schema `memory_dev`
- `~/soulman-prod/memory/` → use schema `memory_prod`

The plan uses `memory` as a placeholder. Substitute it with the correct schema name for the directory you're running from. All table references (`memory.raw_inputs`, `memory.episodes`, etc.) should use the resolved schema name.