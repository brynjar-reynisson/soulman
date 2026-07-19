# cli — Operational Notes

## Subcommands

- `soulman note "<text>"` — appends directly to today's daily report (mechanical, via a `cli-note`-channel rule in `thinking-svc`, no LLM).
- `soulman "<text>"` (no `note`) — general stimulus on the `cli` channel, for future goal-driven reasoning; no rule matches it yet, so today it's only logged to Memory's raw input log.
- `soulman inject <file> [--dev]` — POSTs a file's raw bytes, unmodified, to `perception-svc`'s `POST /api/perceive/raw` (debugging tool: inject one controlled test stimulus without a real external event triggering it). No client-side JSON validation — the endpoint owns all validation, by design, since this tool's whole point is precise low-level control including intentionally malformed input if the caller wants it.
- `soulman discord-history --limit N` — reads `DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID` directly from the environment (not through perception-svc), prints recent messages from the "Soulman Reports" bot oldest-first (debugging tool: verify what the pipeline actually sent).

All four hit `:9001` (prod) by default, `:9011` (`soulman-dev`) with `--dev` — except `discord-history`, which talks directly to Discord's API, not to any soulman service.

## Module boundary: deliberate duplication, not a dependency

`cli/` is its own Go module (`cli/go.mod`), with no `replace` directive onto `perception-svc` (unlike `common`, which every service already imports this way). `cli/discordread.go` is a deliberate, explained ~40-line duplication of `perception-svc/discordread`'s logic rather than a new cross-module dependency — adding one just for a single debug subcommand was judged disproportionate. **If `perception-svc/discordread`'s shape changes, `cli/discordread.go` must be updated by hand** — nothing enforces they stay in sync.

## Gotcha inherited from the injection endpoint

`soulman inject`'s payload is sent through to `perception-svc` unmodified — if your test JSON omits `occurred_at`, the server now defaults it to `received_at` (see `perception-svc/NOTES.md`), but if you're constructing a stimulus for a rule that reads `occurred_at` for something meaningful (e.g. matching a specific historical timestamp), you still need to set it explicitly; the default is just "now," not anything smarter.
