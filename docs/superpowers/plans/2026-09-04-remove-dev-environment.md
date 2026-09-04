# Remove Dev Environment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate the dev/prod environment split entirely — fix the three (now four) service-config defaults that silently pointed at dev, strip dev-only code/config/subjects, simplify the build/deploy scripts and the global deploy skill to a single environment, clean dev-mentions out of living documentation, and finally delete the `soulman-dev` runtime directory and its Postgres schemas.

**Architecture:** No architectural change — this is a subtraction across an existing codebase. Each task is either a small, independently-testable code fix, a documentation cleanup pass over a bounded set of files, or (last) an irreversible cleanup step.

**Tech Stack:** Go 1.25 (memory-svc, perception-svc, thinking-svc, action-svc, projects-svc, common, cli), PowerShell 5.1 (build/deploy scripts), NATS JetStream, Postgres (local Supabase instance).

**Spec:** `docs/superpowers/specs/2026-09-04-remove-dev-environment-design.md`

## Global Constraints

- Keep all "prod" naming exactly as-is (`soulman-prod\`, `memory_prod`, `projects_prod`, unprefixed NATS subjects, ports 9001–9007) — this is a subtraction, not a rename.
- Do not edit `docs/superpowers/specs/*.md` or `docs/superpowers/plans/*.md` — historical record, not edited after the fact.
- Do not fix unrelated pre-existing gaps noticed along the way (e.g. `start-everything.ps1` never launches `projects-svc`, `setup-firewall-rules.ps1` never had a `projects-svc` rule, perception-svc/NOTES.md's stale claim about a `LOG_DIR` gap that's already fixed in the real script). Leave these untouched.
- Every code task ends with `go build ./...` and `go test ./...` passing for the affected module(s).
- Task 11 (environment teardown) is destructive and irreversible — it runs last, only after every other task is verified complete.

---

### Task 1: Fix dev-defaulting service configs

Four services currently default a config value to something dev-shaped, relying on an explicit override elsewhere to behave correctly. Fix all four defaults at the source.

**Files:**
- Modify: `memory-svc/config/config.go:50`
- Modify: `memory-svc/config/config_test.go:70-72`
- Modify: `projects-svc/config/config.go:19`
- Modify: `projects-svc/config/config_test.go:21-22`
- Modify: `action-svc/config/config.go:57`
- Modify: `action-svc/config/config_test.go:75-77`
- Modify: `web-svc/config/config.go:96`
- Modify: `web-svc/config/config_test.go:180-198` (rename test)

- [ ] **Step 1: Fix memory-svc's SCHEMA default**

In `memory-svc/config/config.go`, change:
```go
		Schema:               env("SCHEMA", "memory_dev"),
```
to:
```go
		Schema:               env("SCHEMA", "memory_prod"),
```

In `memory-svc/config/config_test.go`, change:
```go
	if cfg.Schema != "memory_dev" {
		t.Errorf("Schema = %q, want memory_dev", cfg.Schema)
	}
```
to:
```go
	if cfg.Schema != "memory_prod" {
		t.Errorf("Schema = %q, want memory_prod", cfg.Schema)
	}
```

- [ ] **Step 2: Fix projects-svc's SCHEMA default**

In `projects-svc/config/config.go`, change:
```go
		Schema:      env("SCHEMA", "projects_dev"),
```
to:
```go
		Schema:      env("SCHEMA", "projects_prod"),
```

In `projects-svc/config/config_test.go`, change:
```go
	if cfg.Schema != "projects_dev" {
		t.Errorf("Schema = %q, want projects_dev", cfg.Schema)
	}
```
to:
```go
	if cfg.Schema != "projects_prod" {
		t.Errorf("Schema = %q, want projects_prod", cfg.Schema)
	}
```

- [ ] **Step 3: Fix action-svc's SOULMAN_ROOT default**

In `action-svc/config/config.go`, change:
```go
		SoulmanRoot:            env("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-dev`),
```
to:
```go
		SoulmanRoot:            env("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-prod`),
```

In `action-svc/config/config_test.go`, change:
```go
	if cfg.SoulmanRoot != `C:\Users\Lenovo\soulman-dev` {
		t.Errorf("SoulmanRoot = %q, want C:\\Users\\Lenovo\\soulman-dev", cfg.SoulmanRoot)
	}
```
to:
```go
	if cfg.SoulmanRoot != `C:\Users\Lenovo\soulman-prod` {
		t.Errorf("SoulmanRoot = %q, want C:\\Users\\Lenovo\\soulman-prod", cfg.SoulmanRoot)
	}
```

- [ ] **Step 4: Fix web-svc's SOULMAN_ROOT default**

In `web-svc/config/config.go`, change:
```go
		SoulmanRoot:        env("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-dev`),
```
to:
```go
		SoulmanRoot:        env("SOULMAN_ROOT", `C:\Users\Lenovo\soulman-prod`),
```

In `web-svc/config/config_test.go`, change the test name and assertion:
```go
func TestLoad_SoulmanRootDefaultsToDevPath(t *testing.T) {
```
to:
```go
func TestLoad_SoulmanRootDefaultsToProdPath(t *testing.T) {
```
and change:
```go
	if cfg.SoulmanRoot != `C:\Users\Lenovo\soulman-dev` {
		t.Errorf("SoulmanRoot = %q", cfg.SoulmanRoot)
	}
```
to:
```go
	if cfg.SoulmanRoot != `C:\Users\Lenovo\soulman-prod` {
		t.Errorf("SoulmanRoot = %q", cfg.SoulmanRoot)
	}
```

- [ ] **Step 5: Run tests for all four modules**

Run: `go test ./... 2>&1` from within each of `memory-svc/`, `projects-svc/`, `action-svc/`, `web-svc/`
Expected: PASS in every module.

- [ ] **Step 6: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add memory-svc/config/config.go memory-svc/config/config_test.go projects-svc/config/config.go projects-svc/config/config_test.go action-svc/config/config.go action-svc/config/config_test.go web-svc/config/config.go web-svc/config/config_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "$(cat <<'EOF'
fix: default memory-svc/projects-svc/action-svc/web-svc config to prod

These four services silently defaulted SCHEMA/SOULMAN_ROOT to dev
values, relying on an explicit override in each prod launcher script
to behave correctly. Part of removing the dev environment entirely.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_018HEJdzLhkZxcERYFxgsWFB
EOF
)"
```

---

### Task 2: Remove dev subjects from JetStream stream provisioning

**Files:**
- Modify: `action-svc/natsclient/consumer.go:54`
- Modify: `action-svc/natsclient/publisher.go:33`
- Modify: `thinking-svc/natsclient/publisher.go:43`
- Modify: `memory-svc/natsconsumer/memory_write_consumer.go:75`

- [ ] **Step 1: Strip the dev subject from each stream's Subjects list**

In `action-svc/natsclient/consumer.go`, change:
```go
		Subjects: []string{"soulman.thinking.request", "soulman.dev.thinking.request"},
```
to:
```go
		Subjects: []string{"soulman.thinking.request"},
```

In `action-svc/natsclient/publisher.go`, change:
```go
		Subjects: []string{"soulman.memory.write", "soulman.dev.memory.write"},
```
to:
```go
		Subjects: []string{"soulman.memory.write"},
```

In `thinking-svc/natsclient/publisher.go`, change:
```go
		Subjects: []string{"soulman.thinking.request", "soulman.dev.thinking.request"},
```
to:
```go
		Subjects: []string{"soulman.thinking.request"},
```

In `memory-svc/natsconsumer/memory_write_consumer.go`, change:
```go
		Subjects: []string{"soulman.memory.write", "soulman.dev.memory.write"},
```
to:
```go
		Subjects: []string{"soulman.memory.write"},
```

- [ ] **Step 2: Build and test all four modules**

Run: `go build ./... && go test ./...` from within each of `action-svc/`, `thinking-svc/`, `memory-svc/`
Expected: PASS (these are unit tests — no live NATS server needed for `go build`/most `go test` here; live-NATS integration tests are gated behind `SOULMAN_NATS_INTEGRATION_TESTS=1` and are not required to pass for this change).

- [ ] **Step 3: Remove the dev subject from the manually-provisioned STIMULUS stream**

The `STIMULUS` stream is provisioned by hand via the `nats` CLI, not in Go code. Check its current subjects, then remove the dev one:

```bash
nats stream info STIMULUS -j | grep -A3 '"subjects"'
```

If `soulman.dev.stimulus.raw` appears in the list, update it:

```bash
nats stream edit STIMULUS --subjects "soulman.stimulus.raw" -f
```

Verify:

```bash
nats stream info STIMULUS -j | grep -A3 '"subjects"'
```

Expected: only `soulman.stimulus.raw` remains.

- [ ] **Step 4: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add action-svc/natsclient/consumer.go action-svc/natsclient/publisher.go thinking-svc/natsclient/publisher.go memory-svc/natsconsumer/memory_write_consumer.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "$(cat <<'EOF'
fix: remove soulman.dev.* subjects from JetStream stream provisioning

THINKING_REQUEST and MEMORY_WRITE no longer list a dev subject
variant; STIMULUS's manually-provisioned subject list was updated to
match via the nats CLI. Part of removing the dev environment.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_018HEJdzLhkZxcERYFxgsWFB
EOF
)"
```

---

### Task 3: Remove cli's `--dev` flag

The `soulman` CLI has a real `--dev` flag that switches its target from `:9001` to `:9011`. This is functional dev-specific code, not just documentation.

**Files:**
- Modify: `cli/args.go`
- Modify: `cli/args_test.go`
- Modify: `cli/main.go`
- Modify: `cli/schoolbackfill.go`
- Modify: `cli/NOTES.md`

- [ ] **Step 1: Remove the `Dev` field and `--dev` parsing from args.go**

In `cli/args.go`, change the struct:
```go
type parsedArgs struct {
	Text                 string
	Mode                 string
	Priority             string
	Dev                  bool
	InjectFile           string
	DiscordHistoryLimit  int
	SchoolBackfillSince  string
	SchoolBackfillDryRun bool
}
```
to:
```go
type parsedArgs struct {
	Text                 string
	Mode                 string
	Priority             string
	InjectFile           string
	DiscordHistoryLimit  int
	SchoolBackfillSince  string
	SchoolBackfillDryRun bool
}
```

Change the doc comment:
```go
//	soulman "<text>"                      -> Mode: stimulus
//	soulman note "<text>"                  -> Mode: note
//	soulman [--priority P] [--dev] ...     -> flags may appear anywhere
```
to:
```go
//	soulman "<text>"                      -> Mode: stimulus
//	soulman note "<text>"                  -> Mode: note
//	soulman [--priority P] ...             -> flags may appear anywhere
```

Remove the `--dev` case:
```go
		case !endOfFlags && a == "--dev":
			res.Dev = true
		case !endOfFlags && a == "--priority":
```
becomes:
```go
		case !endOfFlags && a == "--priority":
```

Change the usage-error string:
```go
		return parsedArgs{}, fmt.Errorf(`usage: soulman [--dev] [--priority low|normal|high|critical] [note] "<text>"`)
```
to:
```go
		return parsedArgs{}, fmt.Errorf(`usage: soulman [--priority low|normal|high|critical] [note] "<text>"`)
```

- [ ] **Step 2: Remove Dev-related test coverage from args_test.go**

In `cli/args_test.go`, remove this check from `TestParseArgs_PlainText_DefaultsToStimulusMode`:
```go
	if got.Dev {
		t.Error("Dev = true, want false (default)")
	}
```
so the function ends right after the `Priority` check.

Delete the entire `TestParseArgs_DevFlag` function:
```go
func TestParseArgs_DevFlag(t *testing.T) {
	got, err := parseArgs([]string{"--dev", "hello"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !got.Dev {
		t.Error("Dev = false, want true")
	}
}

```

Delete the entire `TestParseArgs_InjectMode_WithDevFlag` function:
```go
func TestParseArgs_InjectMode_WithDevFlag(t *testing.T) {
	got, err := parseArgs([]string{"--dev", "inject", "path/to/file.json"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.Mode != "inject" || got.InjectFile != "path/to/file.json" || !got.Dev {
		t.Errorf("got %+v, want Mode=inject InjectFile=path/to/file.json Dev=true", got)
	}
}

```

- [ ] **Step 3: Remove the dev/prod URL switch from main.go**

In `cli/main.go`, change:
```go
const (
	prodURL = "http://localhost:9001"
	devURL  = "http://localhost:9011"
)

func main() {
	args, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	baseURL := prodURL
	if args.Dev {
		baseURL = devURL
	}
```
to:
```go
const baseURL = "http://localhost:9001"

func main() {
	args, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
```

- [ ] **Step 4: Drop `[--dev]` from schoolbackfill.go's doc comment**

In `cli/schoolbackfill.go`, change:
```go
// runSchoolBackfill implements `soulman school-backfill --since YYYY-MM-DD
// [--dry-run] [--dev]`: a one-off historical scan of @reykjavik.is mail,
```
to:
```go
// runSchoolBackfill implements `soulman school-backfill --since YYYY-MM-DD
// [--dry-run]`: a one-off historical scan of @reykjavik.is mail,
```

- [ ] **Step 5: Run tests**

Run: `go build ./... && go test ./...` from within `cli/`
Expected: PASS.

- [ ] **Step 6: Update cli/NOTES.md**

In `cli/NOTES.md`, change:
```
- `soulman inject <file> [--dev]` — POSTs a file's raw bytes, unmodified, to `perception-svc`'s `POST /api/perceive/raw` (debugging tool: inject one controlled test stimulus without a real external event triggering it). No client-side JSON validation — the endpoint owns all validation, by design, since this tool's whole point is precise low-level control including intentionally malformed input if the caller wants it.
```
to:
```
- `soulman inject <file>` — POSTs a file's raw bytes, unmodified, to `perception-svc`'s `POST /api/perceive/raw` (debugging tool: inject one controlled test stimulus without a real external event triggering it). No client-side JSON validation — the endpoint owns all validation, by design, since this tool's whole point is precise low-level control including intentionally malformed input if the caller wants it.
```

And change:
```
The first four hit `:9001` (prod) by default, `:9011` (`soulman-dev`) with `--dev` — except `discord-history`, which talks directly to Discord's API, not to any soulman service. `school-backfill` also defaults to prod (`:9001`) and respects `--dev` to target dev's perception-svc (`:9011`).
```
to:
```
The first four hit `:9001` by default — except `discord-history`, which talks directly to Discord's API, not to any soulman service. `school-backfill` also defaults to `:9001`.
```

- [ ] **Step 7: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add cli/args.go cli/args_test.go cli/main.go cli/schoolbackfill.go cli/NOTES.md
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "$(cat <<'EOF'
feat(cli): remove --dev flag

The soulman CLI no longer supports targeting a dev perception-svc
instance. Part of removing the dev environment entirely.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_018HEJdzLhkZxcERYFxgsWFB
EOF
)"
```

---

### Task 4: Delete config/dev.json and clean dev-referencing code comments

**Files:**
- Delete: `config/dev.json`
- Modify: `common/sharedconfig/config.go`
- Modify: `perception-svc/config/config.go:128-134`

- [ ] **Step 1: Delete config/dev.json**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" rm config/dev.json
```

- [ ] **Step 2: Clean dev-referencing doc comments in common/sharedconfig/config.go**

Change the package doc comment:
```go
// Package sharedconfig loads the non-secret settings shared across
// Soulman's services from a per-environment JSON file (config/dev.json,
// config/prod.json in the vault; copied to <env-root>\config.json at
// launch by each run-<svc>.ps1 script). Secrets never belong here — they
// stay in .env, which is deliberately kept outside the git-tracked vault.
package sharedconfig
```
to:
```go
// Package sharedconfig loads the non-secret settings shared across
// Soulman's services from config/prod.json in the vault, copied to
// <env-root>\config.json at launch by each run-<svc>.ps1 script. Secrets
// never belong here — they stay in .env, which is deliberately kept
// outside the git-tracked vault.
package sharedconfig
```

Change the `GmailConfig` doc comment:
```go
// GmailConfig holds perception-svc's Gmail channel settings: the search
// query used to find matching messages, the label applied to mark them
// processed (Gmail's own labels are the dedup checkpoint — no local state
// file), and how often to poll. Both dev and prod populate this — only the
// query/seen_label values differ, since both watch the same real inbox and
// each marks what it processes with its own label so neither re-processes
// the other's work.
```
to:
```go
// GmailConfig holds perception-svc's Gmail channel settings: the search
// query used to find matching messages, the label applied to mark them
// processed (Gmail's own labels are the dedup checkpoint — no local state
// file), and how often to poll.
```

Change the `SchoolConfig` doc comment:
```go
// SchoolConfig holds the school-email-events feature's settings, shared
// between thinking-svc (SenderDomains, Enabled) and action-svc (Enabled,
// NotifyTime, CalendarRecipientEmails). Enabled is false in dev.json and
// true in prod.json — dev and prod poll the same real inbox, so running
// this feature in both would create duplicate Calendar invites and
// duplicate Discord messages. See
// docs/superpowers/specs/2026-09-03-school-email-events-design.md.
```
to:
```go
// SchoolConfig holds the school-email-events feature's settings, shared
// between thinking-svc (SenderDomains, Enabled) and action-svc (Enabled,
// NotifyTime, CalendarRecipientEmails). Enabled is true in prod.json for
// the real school-email deployment. See
// docs/superpowers/specs/2026-09-03-school-email-events-design.md.
```

- [ ] **Step 3: Clean the dev-referencing comment in perception-svc/config/config.go**

Change:
```go
		// LOG_DIR is not currently set by perception-svc's run-perception-svc.ps1
		// launchers in soulman-dev/soulman-prod (verified against the live
		// scripts while writing this plan) — only memory-svc's launcher sets
		// it, for its own unrelated file-log purpose. This default lets local
		// `go run .` work out of the box; see this plan's self-review for the
		// one-line manual addition needed in both environments' launcher
		// scripts before this channel finds real sibling logs.
```
to:
```go
		// LOG_DIR is not currently set by perception-svc's run-perception-svc.ps1
		// launcher (verified against the live script while writing this plan) —
		// only memory-svc's launcher sets it, for its own unrelated file-log
		// purpose. This default lets local `go run .` work out of the box; see
		// this plan's self-review for the one-line manual addition needed in
		// the launcher script before this channel finds real sibling logs.
```

- [ ] **Step 4: Build and test**

Run: `go build ./... && go test ./...` from within `common/` and `perception-svc/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add -A config/dev.json common/sharedconfig/config.go perception-svc/config/config.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "$(cat <<'EOF'
chore: delete config/dev.json, clean dev-referencing code comments

config/prod.json is now the only shared config file. Doc comments in
common/sharedconfig and perception-svc/config that explained the
dev/prod split are updated to describe the single environment.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_018HEJdzLhkZxcERYFxgsWFB
EOF
)"
```

---

### Task 5: Simplify build/deploy pipeline scripts

**Files:**
- Modify: `start-everything.ps1`
- Modify: `setup-firewall-rules.ps1`
- Modify: `C:\Users\Lenovo\soulman-prod\run-memory-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-prod\run-projects-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-prod\run-action-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-prod\run-web-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-prod\run-perception-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-prod\run-thinking-svc.ps1`
- Modify: `C:\Users\Lenovo\soulman-prod\run-web.ps1`

Note: the six `run-*.ps1` files under `C:\Users\Lenovo\soulman-prod\` live outside this git repo (that directory is not a git repo — see `CLAUDE.md`). Edit them directly with a text editor or `Set-Content`; there is nothing to `git add`/commit for these files.

- [ ] **Step 1: Update start-everything.ps1's header comment and service loop**

In `start-everything.ps1` (repo root), change:
```powershell
# Master startup script - runs once at Windows login via a single Startup
# shortcut ("Start Everything.lnk"). Launches every shortcut that used to
# live directly in the Startup folder (now moved to old-individual-shortcuts\
# so Windows doesn't also auto-run them a second time) plus all four Soulman
# services in both the dev and prod environments.
```
to:
```powershell
# Master startup script - runs once at Windows login via a single Startup
# shortcut ("Start Everything.lnk"). Launches every shortcut that used to
# live directly in the Startup folder (now moved to old-individual-shortcuts\
# so Windows doesn't also auto-run them a second time) plus every Soulman
# service.
```

Change:
```powershell
foreach ($svc in @("memory-svc", "perception-svc", "thinking-svc", "action-svc", "web-svc", "web")) {
    Start-SoulmanService -Name $svc -Root "C:\Users\Lenovo\soulman-dev"
    Start-SoulmanService -Name $svc -Root "C:\Users\Lenovo\soulman-prod"
}
```
to:
```powershell
foreach ($svc in @("memory-svc", "perception-svc", "thinking-svc", "action-svc", "web-svc", "web")) {
    Start-SoulmanService -Name $svc -Root "C:\Users\Lenovo\soulman-prod"
}
```

- [ ] **Step 2: Update setup-firewall-rules.ps1's header comment and envs list**

In `setup-firewall-rules.ps1` (repo root), change:
```powershell
# One-time setup: pre-creates Windows Firewall inbound-allow rules for every
# Soulman service executable (dev + prod) so Windows never has to prompt for
# them again after a rebuild+restart.
```
to:
```powershell
# One-time setup: pre-creates Windows Firewall inbound-allow rules for every
# Soulman service executable so Windows never has to prompt for them again
# after a rebuild+restart.
```

Change:
```powershell
$envs = @(
    @{ Label = "dev";  Root = "C:\Users\Lenovo\soulman-dev" },
    @{ Label = "prod"; Root = "C:\Users\Lenovo\soulman-prod" }
)
```
to:
```powershell
$envs = @(
    @{ Label = "prod"; Root = "C:\Users\Lenovo\soulman-prod" }
)
```

- [ ] **Step 3: Simplify soulman-prod\run-memory-svc.ps1**

Change the header comment from:
```powershell
# Builds memory-svc from the vault source and runs it in this (prod) environment.
```
to:
```powershell
# Builds memory-svc from the vault source and runs it.
```

Change:
```powershell
$env:SCHEMA = "memory_prod"
$env:LOG_DIR = Join-Path $PSScriptRoot "logs"
New-Item -ItemType Directory -Force $env:LOG_DIR | Out-Null

Write-Warning "memory_prod schema does not exist yet in the local Postgres instance - inserts will fail and fall back to file-only logging until it's created."

& $exe
```
to:
```powershell
$env:LOG_DIR = Join-Path $PSScriptRoot "logs"
New-Item -ItemType Directory -Force $env:LOG_DIR | Out-Null

& $exe
```

(The `SCHEMA` override is now redundant — Task 1 made `memory_prod` the code default. The warning about the schema not existing is stale — `memory-svc/NOTES.md` already documents it was created by hand on 2026-08-08.)

- [ ] **Step 4: Simplify soulman-prod\run-projects-svc.ps1**

Change the header comment from:
```powershell
# Builds projects-svc from the vault source and runs it in this (prod) environment.
```
to:
```powershell
# Builds projects-svc from the vault source and runs it.
```

Change:
```powershell
$env:SCHEMA = "projects_prod"
# HTTP_PORT (9006) and NOTIFY_PORT (9007) use their built-in defaults, which
# are already the prod values -- no override needed here.

& $exe
```
to:
```powershell
# SCHEMA, HTTP_PORT (9006), and NOTIFY_PORT (9007) all use their built-in
# defaults -- no override needed here.

& $exe
```

- [ ] **Step 5: Simplify soulman-prod\run-action-svc.ps1**

Change the header comment from:
```powershell
# Builds action-svc from the vault source and runs it in this (prod) environment.
```
to:
```powershell
# Builds action-svc from the vault source and runs it.
```

Remove:
```powershell
# config.go's SOULMAN_ROOT default points at soulman-dev - must override
# explicitly here or prod would write reports into the dev tree.
$env:SOULMAN_ROOT = $PSScriptRoot

$configSrc = "C:\Users\Lenovo\Documents\Obsidian\soulman\config\prod.json"
```
so that line becomes:
```powershell
$configSrc = "C:\Users\Lenovo\Documents\Obsidian\soulman\config\prod.json"
```
(i.e. delete the comment and the `$env:SOULMAN_ROOT = $PSScriptRoot` line plus the blank line after it — Task 1 made `C:\Users\Lenovo\soulman-prod` the code default, matching `$PSScriptRoot` here.)

- [ ] **Step 6: Simplify soulman-prod\run-web-svc.ps1**

Change the header comment from:
```powershell
# Builds web-svc from the vault source and runs it in this (prod) environment.
```
to:
```powershell
# Builds web-svc from the vault source and runs it.
```

Remove:
```powershell
# config.go's SOULMAN_ROOT default points at soulman-dev - must override
# explicitly here or prod would read reports from the dev tree.
$env:SOULMAN_ROOT = $PSScriptRoot

$configSrc = "C:\Users\Lenovo\Documents\Obsidian\soulman\config\prod.json"
```
so that line becomes:
```powershell
$configSrc = "C:\Users\Lenovo\Documents\Obsidian\soulman\config\prod.json"
```

- [ ] **Step 7: Update header comments only in run-perception-svc.ps1 and run-thinking-svc.ps1**

In `soulman-prod\run-perception-svc.ps1`, change:
```powershell
# Builds perception-svc from the vault source and runs it in this (prod) environment.
```
to:
```powershell
# Builds perception-svc from the vault source and runs it.
```

In `soulman-prod\run-thinking-svc.ps1`, change:
```powershell
# Builds thinking-svc from the vault source and runs it in this (prod) environment.
```
to:
```powershell
# Builds thinking-svc from the vault source and runs it.
```

- [ ] **Step 8: Update run-web.ps1's header comment**

In `soulman-prod\run-web.ps1`, change:
```powershell
# Mirrors the web/ frontend source from the vault into this (prod) environment's
# own directory, builds it, then serves the build via Vite preview. A private
# copy (excluding node_modules/dist) keeps prod's node_modules and build output
# fully isolated from dev's, mirroring how each Go service already builds into
# its own environment's bin/ directory rather than sharing one binary.
```
to:
```powershell
# Mirrors the web/ frontend source from the vault into its own directory,
# builds it, then serves the build via Vite preview. A private copy
# (excluding node_modules/dist) keeps installed dependencies and build
# output isolated from the vault source, mirroring how each Go service
# already builds into its own bin/ directory rather than sharing one binary.
```

- [ ] **Step 9: Verify every prod service still starts cleanly**

Run each launcher and confirm it starts with no fatal error and no `SCHEMA`/`SOULMAN_ROOT` set beforehand in the shell:

```powershell
powershell -NoProfile -File "C:\Users\Lenovo\soulman-prod\run-memory-svc.ps1"
```
(repeat for `perception-svc`, `thinking-svc`, `action-svc`, `web-svc`, `projects-svc`, each in its own terminal/background job, then stop them — or use the `deploy-soulman-services` skill's `Restart-SoulmanService` pattern once Task 6 has rewritten it). Confirm via `Get-Content <root>\logs\<svc>-startup-err.log -Tail 20` that no `SCHEMA`/`SOULMAN_ROOT`-related error appears.

- [ ] **Step 10: Commit the repo-tracked files**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add start-everything.ps1 setup-firewall-rules.ps1
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "$(cat <<'EOF'
chore: build/deploy scripts target soulman-prod only

start-everything.ps1 and setup-firewall-rules.ps1 no longer reference
soulman-dev. The six run-<svc>.ps1 launchers in soulman-prod (outside
this repo) were also simplified now that their SCHEMA/SOULMAN_ROOT
overrides are redundant with the fixed code defaults.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_018HEJdzLhkZxcERYFxgsWFB
EOF
)"
```

---

### Task 6: Rewrite the deploy-soulman-services skill for a single environment

**Files:**
- Modify: `C:\Users\Lenovo\.claude\skills\deploy-soulman-services\SKILL.md` (global skill, outside this repo — no git commit for this file)

- [ ] **Step 1: Replace the entire file content**

Write the following complete content to `C:\Users\Lenovo\.claude\skills\deploy-soulman-services\SKILL.md`, replacing everything currently there:

```markdown
---
name: deploy-soulman-services
description: Use when restarting, rebuilding, or deploying any Soulman service (memory-svc, perception-svc, thinking-svc, action-svc, web-svc, projects-svc, web) in soulman-prod — or before running any robocopy/copy/rm/Remove-Item/Copy-Item command that touches C:\Users\Lenovo\soulman-prod.
---

# Deploying Soulman Services

## The rule

**Never hand-roll a file-sync or delete command against `soulman-prod`. Always invoke the existing `run-<svc>.ps1` script for that service.**

Every service already has a working, `.env`-safe launcher at `C:\Users\Lenovo\soulman-prod\run-<svc>.ps1`. These are the only sanctioned way to rebuild/redeploy a service. `start-everything.ps1` (`C:\Users\Lenovo\start-everything.ps1`) just calls all six of them at login.

**Why:** `soulman-prod` is not a git repo — there is no commit history, no `git status`, no undo. A past incident: `web`'s launcher ran `robocopy $vaultSrc $localSrc /MIR` without excluding `.env`, and `/MIR` deleted the real `.env` (Supabase secrets) from the environment directory on every restart — silently broke auth, blanked the dashboard, only caught and fixed after the fact by restoring from a backup. The fix (`/XF .env*`) is now baked into `run-web.ps1` — but that protection only exists *inside that script*. Any command that bypasses it (a manual `robocopy`, `Copy-Item -Force`, `Remove-Item -Recurse`, or `git clean` run directly against the environment directory) has no such protection and can reproduce the exact same failure, or worse.

## Rebuild + restart one Go service (memory-svc, perception-svc, thinking-svc, action-svc, web-svc, projects-svc)

The running process must be stopped first — Windows won't let `go build` overwrite a `.exe` that's still running, and a stale process would otherwise keep holding the port.

```powershell
function Restart-SoulmanService {
    param([string]$Name, [string]$Root = "C:\Users\Lenovo\soulman-prod")
    $exePath = Join-Path $Root "bin\$Name.exe"
    Get-Process $Name -ErrorAction SilentlyContinue |
        Where-Object { $_.Path -eq $exePath } | Stop-Process -Force
    $logDir = Join-Path $Root "logs"
    New-Item -ItemType Directory -Force $logDir | Out-Null
    Start-Process -FilePath "powershell.exe" `
        -ArgumentList @("-NoProfile","-NonInteractive","-ExecutionPolicy","Bypass","-File",(Join-Path $Root "run-$Name.ps1")) `
        -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $logDir "$Name-startup.log") `
        -RedirectStandardError (Join-Path $logDir "$Name-startup-err.log")
}

# Example: restart web-svc
Restart-SoulmanService -Name web-svc
```

This is the exact same `Start-Process` invocation `start-everything.ps1` uses — only the `Stop-Process` step is new, needed because this runs mid-session rather than at a fresh login. Swap `$Name` for `memory-svc`/`perception-svc`/`thinking-svc`/`action-svc`/`projects-svc` as needed. Check the result: `Start-Sleep 2; Get-Content (Join-Path $Root "logs\$Name-startup-err.log") -Tail 20`.

## The `web` frontend (React/Vite) — usually needs no restart at all

The Vite preview server (`npm run build && npm run preview`) requires an explicit restart after any change — unlike a dev server, it doesn't hot-reload. Only restart after changing `vite.config.ts`, `package.json` dependencies, `web/src/**`, or `.env`-backed `VITE_*` vars.

If a restart is genuinely needed: still go through `run-web.ps1` (`Restart-SoulmanService -Name web` works the same way — `run-web.ps1` does its own internal `robocopy ... /XF .env*` correctly). Do **not** try to kill the underlying `node.exe` processes directly — many unrelated `node.exe` processes run on this machine (other IdeaProjects, editor tooling) and there's no reliable name-based match; let the OS reuse the port or restart via `start-everything.ps1` instead if a stop is truly required.

## Red flags — stop and use the launcher instead

- "I'll just robocopy the vault into `soulman-prod` directly, it's faster" — no. Always `run-<svc>.ps1`.
- "It's just a quick config copy, `.env` won't be touched" — `/MIR` and `Remove-Item -Recurse` don't know that; only `run-web.ps1`'s explicit `/XF .env*` protects it, and only when that exact script runs.
- "I'll `git clean` the environment directory to reset it" — `soulman-prod` isn't a git repo; there is nothing to reset *from*, only files to lose.
```

- [ ] **Step 2: Verify**

Run: `grep -i "soulman-dev\|dev environment" "C:\Users\Lenovo\.claude\skills\deploy-soulman-services\SKILL.md"`
Expected: no output.

---

### Task 7: Clean CLAUDE.md

**Files:**
- Modify: `CLAUDE.md` (repo root)

- [ ] **Step 1: Remove the sync-soulman-dev.cmd row from the Repository Structure table**

Change:
```
| `.agent-suite-mcp.json`         | Claude Code MCP config — obsidian and filesystem servers for this vault         |
| `sync-soulman-dev.cmd`          | Syncs design artifacts from vault → `~/soulman-dev/memory/`                     |
| `sync-soulman-prod.cmd`         | Syncs design artifacts from vault → `~/soulman-prod/memory/`                    |
```
to:
```
| `.agent-suite-mcp.json`         | Claude Code MCP config — obsidian and filesystem servers for this vault         |
| `sync-soulman-prod.cmd`         | Syncs design artifacts from vault → `~/soulman-prod/memory/`                    |
```

- [ ] **Step 2: Reword the school_email rule's "prod-only" framing**

Change:
```
`school_email` (new, 2026-09-03) → matches `@reykjavik.is` senders ahead of the generic gmail rule (prod-only via `school.enabled`), extracts actionable dates/times via a dedicated DeepSeek prompt resolved against the email's own received date.
```
to:
```
`school_email` (new, 2026-09-03) → matches `@reykjavik.is` senders ahead of the generic gmail rule (config-gated via `school.enabled`), extracts actionable dates/times via a dedicated DeepSeek prompt resolved against the email's own received date.
```

- [ ] **Step 3: Clean action-svc's dev/prod mentions**

Change:
```
`DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID` are non-fatal if blank (Send fails, retried/logged like any other notifier failure) — configured in dev and prod as of 2026-07-18 (a dedicated "Soulman Reports" bot).
```
to:
```
`DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID` are non-fatal if blank (Send fails, retried/logged like any other notifier failure) — configured as of 2026-07-18 (a dedicated "Soulman Reports" bot).
```

Change:
```
As of 2026-07-19, `feign_mode` is `true` in both `config/dev.json` and `config/prod.json`, so outbound sends are currently recorded to `logs/feigned-actions.jsonl` instead of actually happening — see `action-svc/NOTES.md`.
```
to:
```
As of 2026-07-19, `feign_mode` is `true` in `config/prod.json`, so outbound sends are currently recorded to `logs/feigned-actions.jsonl` instead of actually happening — see `action-svc/NOTES.md`.
```

- [ ] **Step 4: Clean projects-svc's dev port mentions**

Change:
```
Two HTTP listeners, both bound to `127.0.0.1` only: a main CRUD port (`9006` prod / `9016` dev, proxied by `web-svc` at `/api/projects/**` behind the usual owner-JWT gate) and a notify port (`9007` prod / `9017` dev) the spawned sessions curl back on to report state transitions.
```
to:
```
Two HTTP listeners, both bound to `127.0.0.1` only: a main CRUD port (`9006`, proxied by `web-svc` at `/api/projects/**` behind the usual owner-JWT gate) and a notify port (`9007`) the spawned sessions curl back on to report state transitions.
```

- [ ] **Step 5: Replace the "Running dev and prod simultaneously" section**

Change the entire section (everything from the `### Running dev and prod simultaneously` heading through the paragraph ending in "...the dependency-health blind spot it exposed."):
```
### Running dev and prod simultaneously

Dev and prod share one local NATS server and one local Postgres instance. Each service's NATS subjects, JetStream durable consumer name, and HTTP port are configurable — subjects and `consumer_names` (`memory_svc`, `thinking_svc`, `action_svc`) come from the shared config file, `HTTP_PORT` from env. Prod keeps unprefixed defaults (`soulman.stimulus.raw`, ports `9001`-`9004`); dev uses `soulman.dev.*` subjects, `*-dev` consumer names, and ports `9011`-`9014`.

**This is essential, not cosmetic**: JetStream identifies a durable consumer by `(stream, name)`, so two environments reusing the same consumer name on the same stream would silently steal each other's messages — every consumer also sets `FilterSubject` so it only ever sees its own environment's traffic. `STIMULUS` (provisioned manually via the `nats` CLI), `THINKING_REQUEST`, and `MEMORY_WRITE` (both idempotently created/updated in code via `CreateOrUpdateStream`) are all durable JetStream streams, each one's subject list covering both the prod and `soulman.dev.*` variants.

`web-svc` follows the same port convention (`9005` prod / `9015` dev) but has no JetStream consumer and no NATS subscription at all — it only makes outbound HTTP calls to the other four services and reads report files directly off disk, so it needs no `consumer_names` entry and isn't part of the STIMULUS/THINKING_REQUEST/MEMORY_WRITE stream discussion above.

`soulman-prod/` mirrors `soulman-dev/`'s layout exactly but was provisioned later. `memory_prod`'s Postgres schema (`raw_inputs` + `episodes`, same DDL as `memory_dev` — see `docs/superpowers/specs/sql/`) was created by hand on 2026-08-08, after going unnoticed long enough to balloon `memory-svc-startup-err.log` to 11M lines / 2.46GB via infinite NATS redelivery retries — see `memory-svc/NOTES.md`'s incident writeup for the full story and the dependency-health blind spot it exposed.
```
to:
```
### NATS and Postgres

Every service connects to one local NATS server and one local Postgres instance. Each service's NATS subjects, JetStream durable consumer name, and HTTP port are configurable — subjects and `consumer_names` (`memory_svc`, `thinking_svc`, `action_svc`) come from the shared config file, `HTTP_PORT` from env. Unprefixed subjects (`soulman.stimulus.raw`) and ports `9001`-`9004` are used throughout. `STIMULUS` is provisioned manually via the `nats` CLI; `THINKING_REQUEST` and `MEMORY_WRITE` are both idempotently created/updated in code via `CreateOrUpdateStream`. All three are durable JetStream streams.

`web-svc` uses port `9005` but has no JetStream consumer and no NATS subscription at all — it only makes outbound HTTP calls to the other four services and reads report files directly off disk, so it needs no `consumer_names` entry and isn't part of the STIMULUS/THINKING_REQUEST/MEMORY_WRITE stream discussion above.

`memory_prod`'s Postgres schema (`raw_inputs` + `episodes`, DDL in `docs/superpowers/specs/sql/`) was created by hand on 2026-08-08, after going unnoticed long enough to balloon `memory-svc-startup-err.log` to 11M lines / 2.46GB via infinite NATS redelivery retries — see `memory-svc/NOTES.md`'s incident writeup for the full story and the dependency-health blind spot it exposed.
```

- [ ] **Step 6: Update the Startup section**

Change:
```
`C:\Users\Lenovo\start-everything.ps1` (via `Start Everything.lnk` in the Windows Startup folder) builds and starts all five services (including `web-svc`) in both `soulman-dev` and `soulman-prod` on every login — a git pull here is picked up on the next login without a separate deploy step.
```
to:
```
`C:\Users\Lenovo\start-everything.ps1` (via `Start Everything.lnk` in the Windows Startup folder) builds and starts all five services (including `web-svc`) in `soulman-prod` on every login — a git pull here is picked up on the next login without a separate deploy step.
```

Change:
```
`start-everything.ps1` also runs `web` (the frontend) per environment via the same generic launcher, but its `run-web.ps1` differs from the Go services' pattern: instead of building in place, it `robocopy /MIR`s `web/` from the vault into a private per-environment copy (`<env-root>\web\`, excluding `node_modules`/`dist`) before running `npm ci`, so dev's and prod's installed dependencies and build output never collide — mirroring the isolation each Go service already gets from building into its own `bin/`. Dev then runs `npm run dev` (Vite dev server); prod runs `npm run build && npm run preview`.
```
to:
```
`start-everything.ps1` also runs `web` (the frontend) via the same generic launcher, but its `run-web.ps1` differs from the Go services' pattern: instead of building in place, it `robocopy /MIR`s `web/` from the vault into its own private copy (`soulman-prod\web\`, excluding `node_modules`/`dist`) before running `npm ci`, mirroring the isolation each Go service already gets from building into its own `bin/`. It then runs `npm run build && npm run preview`.
```

- [ ] **Step 7: Replace the "Two Environments" section**

Change:
```
## Two Environments

| Env | Path | Supabase Schema |
|-----|------|-----------------|
| Dev | `~/soulman-dev/memory/` | `memory_dev` |
| Prod | `~/soulman-prod/memory/` | `memory_prod` |

Agent definitions in `memory/.opencode/agent/` resolve the schema based on which directory they're invoked from.
```
to:
```
## Environment

OpenCode's memory-agent workspace lives at `~/soulman-prod/memory/`. Agent definitions in `memory/.opencode/agent/` use the `memory_prod` schema.
```

- [ ] **Step 8: Verify**

Run: `grep -in "soulman-dev\|soulman\.dev\|dev\.json\|memory_dev\|projects_dev\|:901[1-7]" "C:\Users\Lenovo\Documents\obsidian\soulman\CLAUDE.md"`
Expected: no output.

Also confirm the root module design docs need no changes — the grep hits found during brainstorming (`Action module.md`, `Perception module.md`, `Memory module.md`, `Thinking module.md`, `Project Soulman.md`) were all matches on the English word "development" (e.g. Perception module.md's conceptual "Development Mode" section describing a hypothetical future watchdog toggle), not the real `soulman-dev` environment — these docs predate the actual Go implementation and never mention it. No edits needed there; this step is a verification, not a code change.

- [ ] **Step 9: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add CLAUDE.md
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "$(cat <<'EOF'
docs: remove dev/prod dual-environment framing from CLAUDE.md

Drops the stale sync-soulman-dev.cmd table row, the "Running dev and
prod simultaneously" section, and the dual-port/dual-schema "Two
Environments" table, replacing them with single-environment
descriptions. Part of removing the dev environment entirely.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_018HEJdzLhkZxcERYFxgsWFB
EOF
)"
```

---

### Task 8: Clean memory-svc and action-svc NOTES.md

**Files:**
- Modify: `memory-svc/NOTES.md`
- Modify: `action-svc/NOTES.md`

- [ ] **Step 1: Clean memory-svc/NOTES.md**

Change:
```
`natsconsumer/consumer_test.go` and `natsconsumer/memory_write_consumer_test.go` connected to the real shared dev/prod NATS server by default and published test payloads onto the real `soulman.stimulus.raw`/`soulman.memory.write` subjects
```
to:
```
`natsconsumer/consumer_test.go` and `natsconsumer/memory_write_consumer_test.go` connected to the real shared NATS server by default and published test payloads onto the real `soulman.stimulus.raw`/`soulman.memory.write` subjects
```

Change:
```
Both tables are applied by hand once per environment — `episodes` via `docs/superpowers/specs/sql/2026-07-18-episodes-table.sql`, `raw_inputs` via `docs/superpowers/specs/sql/2026-08-08-raw-inputs-table.sql` (added retroactively, see the incident below — no DDL for `raw_inputs` had ever been committed before this, since `memory_dev`'s copy was originally created live by the `soulman-db-builder` OpenCode agent rather than from a checked-in file).
```
to:
```
Both tables are applied by hand — `episodes` via `docs/superpowers/specs/sql/2026-07-18-episodes-table.sql`, `raw_inputs` via `docs/superpowers/specs/sql/2026-08-08-raw-inputs-table.sql` (added retroactively, see the incident below — no DDL for `raw_inputs` had ever been committed before this, since the schema was originally created live by the `soulman-db-builder` OpenCode agent rather than from a checked-in file).
```

Change:
```
`perception-svc`'s `system_monitor` polls this service's `/health` via a new `internal_health` check (`config/dev.json`/`config/prod.json`) and notifies Discord on any dependency's `ok`↔`down` transition — not on every poll while steady, matching the Log Error channel's dedup philosophy.
```
to:
```
`perception-svc`'s `system_monitor` polls this service's `/health` via a new `internal_health` check (`config/prod.json`) and notifies Discord on any dependency's `ok`↔`down` transition — not on every poll while steady, matching the Log Error channel's dedup philosophy.
```

- [ ] **Step 2: Clean action-svc/NOTES.md**

Change:
```
`natsclient/consumer_test.go` and `natsclient/natsclient_test.go` connected to the real shared dev/prod NATS server by default and published test payloads onto the real `soulman.thinking.request`/`soulman.memory.write` subjects
```
to:
```
`natsclient/consumer_test.go` and `natsclient/natsclient_test.go` connected to the real shared NATS server by default and published test payloads onto the real `soulman.thinking.request`/`soulman.memory.write` subjects
```

Change:
```
In dev (`feign_mode: true`), a `"sent"` entry means feign mode recorded it to `feigned-actions.jsonl` and returned success — same transparent-success semantics feign mode already has everywhere else; the audit log doesn't distinguish a feigned send from a real one.
```
to:
```
When `feign_mode` is `true`, a `"sent"` entry means feign mode recorded it to `feigned-actions.jsonl` and returned success — same transparent-success semantics feign mode already has everywhere else; the audit log doesn't distinguish a feigned send from a real one.
```

Change:
```
Verified for real: publish a message while `action-svc` is down, then start it — the message is delivered once it comes back up (see `TestConsumer_SurvivesRestartAfterDowntime`, and confirmed manually against live dev infrastructure).
```
to:
```
Verified for real: publish a message while `action-svc` is down, then start it — the message is delivered once it comes back up (see `TestConsumer_SurvivesRestartAfterDowntime`, and confirmed manually against live infrastructure).
```

Change the entire "Known deferred issue" section:
```
## Known deferred issue

Dev and prod share one Discord bot/channel/token for "Soulman Reports" — every Gmail-triage Discord notification is sent twice (once per environment) since both watch the same real inbox. Real bug, not yet fixed; deliberately deferred rather than addressed as part of the debugging-tools or triage work.
```
to:
```
## Known issue (resolved 2026-09-04 by dev-environment removal)

Dev and prod used to share one Discord bot/channel/token for "Soulman Reports," causing every Gmail-triage Discord notification to send twice (once per environment) since both watched the same real inbox. This is now moot — removing the dev environment (see `docs/superpowers/specs/2026-09-04-remove-dev-environment-design.md`) leaves only one environment, so there is no second bot instance left to double-send from.
```

Change:
```
The `feign_mode` field in `config/dev.json`/`config/prod.json` (currently `true` in both environments — not an environment variable, unlike `REPORT_NOTIFIER`/`DISCORD_BOT_TOKEN`) makes `action-svc` record outbound side effects instead of performing them
```
to:
```
The `feign_mode` field in `config/prod.json` (currently `true` — not an environment variable, unlike `REPORT_NOTIFIER`/`DISCORD_BOT_TOKEN`) makes `action-svc` record outbound side effects instead of performing them
```

Change:
```
**If you're wondering why no Discord messages are arriving:** check `feign_mode` in the running environment's config first, before assuming something's broken. It was turned on deliberately in both dev and prod as of 2026-07-19 — turn it back off (`feign_mode: false` in `config/dev.json`/`config/prod.json`, then restart `action-svc`) when you want real sends again.
```
to:
```
**If you're wondering why no Discord messages are arriving:** check `feign_mode` in the config first, before assuming something's broken. It was turned on deliberately as of 2026-07-19 — turn it back off (`feign_mode: false` in `config/prod.json`, then restart `action-svc`) when you want real sends again.
```

Change:
```
`feign_mode` (already `true` in both `config/dev.json` and `config/prod.json`) governs these sends exactly like it already governs Gmail's — no separate gate was needed.
```
to:
```
`feign_mode` (already `true` in `config/prod.json`) governs these sends exactly like it already governs Gmail's — no separate gate was needed.
```

Change:
```
during the configured window (default `00:00`-`10:00`, local time, `do_not_disturb.enabled`/`.start`/`.end` in `config/dev.json`/`config/prod.json`)
```
to:
```
during the configured window (default `00:00`-`10:00`, local time, `do_not_disturb.enabled`/`.start`/`.end` in `config/prod.json`)
```

Change:
```
This is the prerequisite for turning `feign_mode` off in prod — flipping that flag is a separate, deliberate deployment decision once DND is verified working in dev (both environments have `do_not_disturb.enabled: true` as of this feature, even though dev's sends are still feigned regardless).
```
to:
```
This is the prerequisite for turning `feign_mode` off — flipping that flag is a separate, deliberate deployment decision once DND is verified working for real (`do_not_disturb.enabled: true` as of this feature, even though sends are still feigned regardless).
```

- [ ] **Step 3: Verify**

Run: `grep -in "dev" "C:\Users\Lenovo\Documents\obsidian\soulman\memory-svc\NOTES.md" "C:\Users\Lenovo\Documents\obsidian\soulman\action-svc\NOTES.md"`
Expected: no remaining hits refer to the dev environment (any hit should only be an incidental English word, if any).

- [ ] **Step 4: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add memory-svc/NOTES.md action-svc/NOTES.md
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "$(cat <<'EOF'
docs: remove dev-environment mentions from memory-svc/action-svc NOTES

The dev/prod duplicate-Discord-notification bug is marked resolved by
dev's removal rather than deleted, preserving the incident history.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_018HEJdzLhkZxcERYFxgsWFB
EOF
)"
```

---

### Task 9: Clean perception-svc and thinking-svc NOTES.md

**Files:**
- Modify: `perception-svc/NOTES.md`
- Modify: `thinking-svc/NOTES.md`

- [ ] **Step 1: Clean perception-svc/NOTES.md**

Change:
```
- Dev's `config/dev.json` points at `soulman-dev/test-errors/` specifically so manual/test file drops don't mix with real DigitalMe-generated error files; prod's `config/prod.json` points at the real `C:\Users\Lenovo\DigitalMe\errors`.
```
to:
```
- `config/prod.json`'s `watch_paths` points at the real `C:\Users\Lenovo\DigitalMe\errors`.
```

Change:
```
Both `soulman-dev` and `soulman-prod` poll the **same real Gmail inbox**, sharing one OAuth client/refresh token — each environment dedups via its own Gmail label (`soulman/seen-dev` / `soulman/seen`) rather than separate credentials. A message both environments see over time ends up carrying both labels; this is expected, not a bug.
```
to:
```
Polls the real Gmail inbox via one OAuth client/refresh token, dedupping via its own Gmail label (`soulman/seen`).
```

Change:
```
3. **The backlog incident.** The Gmail query originally had no date bound. Combined with fix #2 above, a restart triggered the async poll to silently process ~2 months of backlog unattended — hundreds of DeepSeek classifications, and (for the ~10% judged important) many duplicate Discord notifications, doubled since dev and prod share one Discord bot/channel (see `action-svc/NOTES.md`'s known-deferred-bug note). Fixed by adding `after:2026/07/17` to both dev's and prod's Gmail queries (`config/dev.json` / `config/prod.json`) — the project's working rule going forward: **don't let a poll-based channel silently reach back further than a bounded, explicit floor.**
```
to:
```
3. **The backlog incident.** The Gmail query originally had no date bound. Combined with fix #2 above, a restart triggered the async poll to silently process ~2 months of backlog unattended — hundreds of DeepSeek classifications, and (for the ~10% judged important) many duplicate Discord notifications. Fixed by adding `after:2026/07/17` to the Gmail query (`config/prod.json`) — the project's working rule going forward: **don't let a poll-based channel silently reach back further than a bounded, explicit floor.**
```

Delete this sentence entirely (its own paragraph, in the System Monitor channel section):
```
Dev and prod both poll the same physical machine's disk/memory/CPU and will each independently detect and alert on the same real condition — the same accepted duplication the Gmail channel already has for the shared inbox.
```

Change:
```
`system_monitor.threshold_grace_period_minutes` (disk_space/memory/cpu) and `service_grace_period_minutes` (service_health, and internal_health for both the top-level reachability check and each dependency) are independent knobs, both `15` in `config/{dev,prod}.json` today — 300s poll interval means 3 consecutive polls.
```
to:
```
`system_monitor.threshold_grace_period_minutes` (disk_space/memory/cpu) and `service_grace_period_minutes` (service_health, and internal_health for both the top-level reachability check and each dependency) are independent knobs, both `15` in `config/prod.json` today — 300s poll interval means 3 consecutive polls.
```

Change:
```
**Deployment gap found while building this feature:** the design spec assumed `LOG_DIR` was already set for every service via its `run-<svc>.ps1` launcher. That's only true for `memory-svc` (which sets it for its own unrelated file-log purpose) — `perception-svc`'s own launcher in both `soulman-dev` and `soulman-prod` does not set `LOG_DIR` today. `perception-svc/config.Load()` defaults it to `./logs` (matching every other env var's local-dev-friendly relative-default pattern in this file), but that only resolves correctly if the process's working directory happens to be the environment root when launched. **Before this channel will find real sibling logs in either environment, add `$env:LOG_DIR = Join-Path $PSScriptRoot "logs"` to `perception-svc`'s `run-perception-svc.ps1` in both `soulman-dev\` and `soulman-prod\`** (mirroring the line `memory-svc`'s launcher already has) — those files live outside this git repo, so this plan cannot make that edit itself.
```
to:
```
**Deployment gap found while building this feature:** the design spec assumed `LOG_DIR` was already set for every service via its `run-<svc>.ps1` launcher. That's only true for `memory-svc` (which sets it for its own unrelated file-log purpose) — `perception-svc`'s own launcher does not set `LOG_DIR` today. `perception-svc/config.Load()` defaults it to `./logs` (matching every other env var's local-dev-friendly relative-default pattern in this file), but that only resolves correctly if the process's working directory happens to be the environment root when launched. **Before this channel will find real sibling logs, add `$env:LOG_DIR = Join-Path $PSScriptRoot "logs"` to `perception-svc`'s `run-perception-svc.ps1`** (mirroring the line `memory-svc`'s launcher already has) — that file lives outside this git repo, so this plan cannot make that edit itself.
```

Change the entire "Known deferred issue" section:
```
## Known deferred issue

Dev and prod share one Discord bot/channel/token for the "Soulman Reports" notifications — a real bug (every Gmail-triage Discord notification is sent twice, once per environment), deliberately not fixed yet. See `action-svc/NOTES.md`.
```
to:
```
## Known issue (resolved 2026-09-04 by dev-environment removal)

Dev and prod used to share one Discord bot/channel/token for the "Soulman Reports" notifications, causing every Gmail-triage Discord notification to send twice (once per environment). Moot now that the dev environment is gone — see `action-svc/NOTES.md`.
```

Change:
```
A fifth `system_monitor` check type, alongside `disk_space`/`memory`/`cpu`/`service_health`: polls a *soulman* service's own `GET /health` (currently only `memory-svc`, at `http://localhost:9002/health` prod / `:9012` dev) and parses its `dependencies` map
```
to:
```
A fifth `system_monitor` check type, alongside `disk_space`/`memory`/`cpu`/`service_health`: polls a *soulman* service's own `GET /health` (currently only `memory-svc`, at `http://localhost:9002/health`) and parses its `dependencies` map
```

Change:
```
**Root cause:** `perception-svc/natspublish/publisher_test.go`, `thinking-svc/natsclient/{consumer,publisher}_test.go`, `memory-svc/natsconsumer/{consumer,memory_write_consumer}_test.go`, and `action-svc/natsclient/{consumer,natsclient}_test.go` — 7 files, 18 test functions total across all four service modules — default `natsURL()` to `nats://localhost:4222` with no test isolation. That's the exact NATS server `soulman-dev` and `soulman-prod` both share.
```
to:
```
**Root cause:** `perception-svc/natspublish/publisher_test.go`, `thinking-svc/natsclient/{consumer,publisher}_test.go`, `memory-svc/natsconsumer/{consumer,memory_write_consumer}_test.go`, and `action-svc/natsclient/{consumer,natsclient}_test.go` — 7 files, 18 test functions total across all four service modules — default `natsURL()` to `nats://localhost:4222` with no test isolation. That's the exact NATS server the running environment uses.
```

Change:
```
- `memory-svc/natsconsumer`'s `memory_write_consumer_test.go` published fake `OutcomeRecord`s onto `soulman.memory.write` — the real memory-svc consumer wrote these as genuine episode rows into Postgres (`memory_dev`/`memory_prod`), a real data-integrity issue, not just log noise.
- **Worst of the four:** `memory-svc/natsconsumer/consumer_test.go`'s `TestMain` unconditionally called `stream.Purge(ctx)` on the real `STIMULUS` and `MEMORY_WRITE` JetStream streams before every test run in that package — silently deleting any real message still unconsumed by prod's/dev's durable consumers at that moment.
```
to:
```
- `memory-svc/natsconsumer`'s `memory_write_consumer_test.go` published fake `OutcomeRecord`s onto `soulman.memory.write` — the real memory-svc consumer wrote these as genuine episode rows into Postgres (`memory_prod`), a real data-integrity issue, not just log noise.
- **Worst of the four:** `memory-svc/natsconsumer/consumer_test.go`'s `TestMain` unconditionally called `stream.Purge(ctx)` on the real `STIMULUS` and `MEMORY_WRITE` JetStream streams before every test run in that package — silently deleting any real message still unconsumed by the durable consumers at that moment.
```

Change:
```
**Deliberately not fixed further**: even with the env var set, these tests still target the real shared dev/prod streams (no per-run subject/stream isolation was added) — running them intentionally still requires care, e.g. not doing so while worried about a pending redelivery in flight.
```
to:
```
**Deliberately not fixed further**: even with the env var set, these tests still target the real shared streams (no per-run subject/stream isolation was added) — running them intentionally still requires care, e.g. not doing so while worried about a pending redelivery in flight.
```

- [ ] **Step 2: Clean thinking-svc/NOTES.md**

Change the heading:
```
## School Email rule: prod-only gating and date resolution (added 2026-09-03)
```
to:
```
## School Email rule: config-gated and date resolution (added 2026-09-03)
```

Change:
```
**The rule is prod-only**: `main.go` conditionally prepends it to `rules.Registry` only if `school.enabled` is true in the config AND `school.sender_domains` is non-empty — the registry itself has no built-in per-environment concept, so environment-specific gating must happen at registration time.
```
to:
```
**The rule is config-gated**: `main.go` conditionally prepends it to `rules.Registry` only if `school.enabled` is true in the config AND `school.sender_domains` is non-empty — the registry itself has no built-in conditional-registration concept of its own, so this gating happens at registration time in `main.go`.
```

Change:
```
`natsclient/consumer_test.go` and `natsclient/publisher_test.go` connected to the real shared dev/prod NATS server by default and published test payloads onto the real `soulman.stimulus.raw`/`soulman.thinking.request` subjects — a routine `go test ./...` produced real Discord noise and log errors in prod.
```
to:
```
`natsclient/consumer_test.go` and `natsclient/publisher_test.go` connected to the real shared NATS server by default and published test payloads onto the real `soulman.stimulus.raw`/`soulman.thinking.request` subjects — a routine `go test ./...` produced real Discord noise and log errors.
```

- [ ] **Step 3: Verify**

Run: `grep -in "dev" "C:\Users\Lenovo\Documents\obsidian\soulman\perception-svc\NOTES.md" "C:\Users\Lenovo\Documents\obsidian\soulman\thinking-svc\NOTES.md"`
Expected: no remaining hits refer to the dev environment.

- [ ] **Step 4: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add perception-svc/NOTES.md thinking-svc/NOTES.md
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "$(cat <<'EOF'
docs: remove dev-environment mentions from perception-svc/thinking-svc NOTES

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_018HEJdzLhkZxcERYFxgsWFB
EOF
)"
```

---

### Task 10: Clean web-svc, common, and projects-svc NOTES.md

**Files:**
- Modify: `web-svc/NOTES.md`
- Modify: `common/NOTES.md`
- Modify: `projects-svc/NOTES.md`

- [ ] **Step 1: Clean web-svc/NOTES.md**

Change:
```
Both are required environment variables (fatal startup error if either is blank), set via `.env` in each of `soulman-dev\` and `soulman-prod\` (loaded by `load-env.ps1`, same as `action-svc`'s Discord token).
```
to:
```
Both are required environment variables (fatal startup error if either is blank), set via `.env` in `soulman-prod\` (loaded by `load-env.ps1`, same as `action-svc`'s Discord token).
```

Change:
```
Set via `.env` in `soulman-dev\` and `soulman-prod\`, same file `SUPABASE_URL` lives in. No shared-config (`config/dev.json`/`prod.json`) entry exists for this — it's a secret, not a `web.*` setting.
```
to:
```
Set via `.env` in `soulman-prod\`, same file `SUPABASE_URL` lives in. No shared-config (`config/prod.json`) entry exists for this — it's a secret, not a `web.*` setting.
```

Change:
```
Unlike `agent-suite`'s `UserResolverFilter` (DB-backed `suite_user`/`user_role` lookup), `web-svc`'s `auth.Verifier` does no database lookup at all — it just compares the JWT's `email` claim against `web.owner_email` in `config/dev.json`/`prod.json`.
```
to:
```
Unlike `agent-suite`'s `UserResolverFilter` (DB-backed `suite_user`/`user_role` lookup), `web-svc`'s `auth.Verifier` does no database lookup at all — it just compares the JWT's `email` claim against `web.owner_email` in `config/prod.json`.
```

Change the heading:
```
## Dashboard is tunneled to `soulman.breynisson.org` (prod only, added 2026-07-21)
```
to:
```
## Dashboard is tunneled to `soulman.breynisson.org` (added 2026-07-21)
```

Change:
```
`cloudflared` maps `https://soulman.breynisson.org` to prod's Vite preview server on `localhost:5191`. Two things had to change for this to work, both prod-only (dev is never tunneled):
```
to:
```
`cloudflared` maps `https://soulman.breynisson.org` to the Vite preview server on `localhost:5191`. Two things had to change for this to work:
```

Change:
```
1. `web/vite.config.ts`'s `preview.allowedHosts` must list `soulman.breynisson.org` — Vite's preview server rejects requests whose `Host` header isn't localhost or in this allowlist (`Blocked request. This host is not allowed.`). This file lives in the vault and is mirrored by `run-web.ps1`'s robocopy into both `soulman-dev\web\` and `soulman-prod\web\`, so the allowlist applies to both environments' preview servers even though only prod is actually tunneled.
```
to:
```
1. `web/vite.config.ts`'s `preview.allowedHosts` must list `soulman.breynisson.org` — Vite's preview server rejects requests whose `Host` header isn't localhost or in this allowlist (`Blocked request. This host is not allowed.`). This file lives in the vault and is mirrored by `run-web.ps1`'s robocopy into `soulman-prod\web\`.
```

Change:
```
Root cause: `VITE_WEB_SVC_URL` was baked in as an absolute `http://localhost:9005` (`9015` in dev), evaluated by the *browser*, not the server.
```
to:
```
Root cause: `VITE_WEB_SVC_URL` was baked in as an absolute `http://localhost:9005`, evaluated by the *browser*, not the server.
```

Change:
```
Applied the same pattern here: `web/vite.config.ts`'s `server.proxy` and `preview.proxy` each forward `/api` to the environment's `web-svc` port (`9015` dev, `9005` prod), and `web/src/api.ts` now calls relative paths (`fetch(path, ...)`) instead of prefixing an absolute `WEB_SVC_URL` — the browser only ever talks to whatever origin served the page.
```
to:
```
Applied the same pattern here: `web/vite.config.ts`'s `server.proxy` and `preview.proxy` each forward `/api` to `web-svc`'s port (`9005`), and `web/src/api.ts` now calls relative paths (`fetch(path, ...)`) instead of prefixing an absolute `WEB_SVC_URL` — the browser only ever talks to whatever origin served the page.
```

Change:
```
What *was* verified pre-merge: built `web-svc` directly from this feature branch into an isolated scratch directory (temp dir, temp port, never touching the real `soulman-dev`/`soulman-prod` processes or `.env`), confirmed it starts with no fatal config error (proving `claude_project_roots` parses from `config/dev.json`) and that both `/api/claude/roots` and `/api/claude/launch` are registered behind auth (401 with no token, matching the httptest-level coverage). See `~/.claude/skills/deploy-soulman-services` for why a live rebuild against the real dev/prod environments is never done by hand-rolling a copy/robocopy command outside their `run-<svc>.ps1` launchers.
```
to:
```
What *was* verified pre-merge: built `web-svc` directly from this feature branch into an isolated scratch directory (temp dir, temp port, never touching the real `soulman-prod` process or `.env`), confirmed it starts with no fatal config error (proving `claude_project_roots` parses from `config/prod.json`) and that both `/api/claude/roots` and `/api/claude/launch` are registered behind auth (401 with no token, matching the httptest-level coverage). See `~/.claude/skills/deploy-soulman-services` for why a live rebuild is never done by hand-rolling a copy/robocopy command outside its `run-<svc>.ps1` launcher.
```

- [ ] **Step 2: Clean common/NOTES.md**

Change:
```
2. `nats_url`, `stimulus_subject`, `thinking_request_subject`, `memory_write_subject`, `consumer_names` (`memory_svc`, `thinking_svc`, later `action_svc`) — moved dev/prod NATS wiring out of per-service env vars into one git-tracked place.
```
to:
```
2. `nats_url`, `stimulus_subject`, `thinking_request_subject`, `memory_write_subject`, `consumer_names` (`memory_svc`, `thinking_svc`, later `action_svc`) — moved NATS wiring out of per-service env vars into one git-tracked place.
```

Change:
```
Pattern for adding a new field: extend the `Config`/nested struct here, add fatal-fast validation matching the existing style (empty string / non-positive int → startup error), add it to both `config/dev.json` and `config/prod.json`, and extend `common/sharedconfig`'s tests for both the populated and zero-value cases.
```
to:
```
Pattern for adding a new field: extend the `Config`/nested struct here, add fatal-fast validation matching the existing style (empty string / non-positive int → startup error), add it to `config/prod.json`, and extend `common/sharedconfig`'s tests for both the populated and zero-value cases.
```

- [ ] **Step 3: Clean projects-svc/NOTES.md**

Change:
```
`HTTP_PORT` (default `9006`) is the main CRUD API — `web-svc` is its only client, always on the same host in this repo's current single-machine deployment model (confirmed via `common/sharedconfig`'s `WebConfig.ProjectsSvcURL`, `http://localhost:...` in both `config/dev.json` and `config/prod.json`).
```
to:
```
`HTTP_PORT` (default `9006`) is the main CRUD API — `web-svc` is its only client, always on the same host in this repo's current single-machine deployment model (confirmed via `common/sharedconfig`'s `WebConfig.ProjectsSvcURL`, `http://localhost:...` in `config/prod.json`).
```

Change:
```
`config.Load()` defaults `SCHEMA` to `projects_dev` (mirrors `memory-svc`'s convention). An omitted override in prod's `run-projects-svc.ps1` would silently make prod write into the dev schema — there is no cross-check that catches this. Always confirm `SCHEMA=projects_prod` is actually set in prod's launcher before trusting prod data.
```
to:
```
`config.Load()` defaults `SCHEMA` to `projects_prod` (mirrors `memory-svc`'s convention, and matches this repo's single-environment deployment — no override is needed in `run-projects-svc.ps1`).
```

Change:
```
`run-projects-svc.ps1` (in both `soulman-dev\` and `soulman-prod\`), applying the DDL against real `projects_dev`/`projects_prod` schemas, and wiring `start-everything.ps1`/`setup-firewall-rules.ps1` for the two new ports are **not** part of this git repo and were explicitly out of scope for `docs/superpowers/plans/2026-09-04-projects-tool.md` — that work happens by hand, after this branch merges, the same way every other service's initial deployment was bootstrapped.
```
to:
```
`run-projects-svc.ps1` (in `soulman-prod\`), applying the DDL against the real `projects_prod` schema, and wiring `start-everything.ps1`/`setup-firewall-rules.ps1` for the two new ports are **not** part of this git repo and were explicitly out of scope for `docs/superpowers/plans/2026-09-04-projects-tool.md` — that work happens by hand, after this branch merges, the same way every other service's initial deployment was bootstrapped.
```

- [ ] **Step 4: Verify**

Run: `grep -in "dev" "C:\Users\Lenovo\Documents\obsidian\soulman\web-svc\NOTES.md" "C:\Users\Lenovo\Documents\obsidian\soulman\common\NOTES.md" "C:\Users\Lenovo\Documents\obsidian\soulman\projects-svc\NOTES.md"`
Expected: no remaining hits refer to the dev environment (the word "deployment" will still match "dev" as a substring — that's fine, it's not a dev-environment reference).

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add web-svc/NOTES.md common/NOTES.md projects-svc/NOTES.md
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "$(cat <<'EOF'
docs: remove dev-environment mentions from web-svc/common/projects-svc NOTES

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_018HEJdzLhkZxcERYFxgsWFB
EOF
)"
```

---

### Task 11: Environment teardown (destructive, run last)

**Only start this task once Tasks 1–10 are complete and verified.** This task is irreversible.

**Files:** none in this repo — operates entirely on `C:\Users\Lenovo\soulman-dev\` (a non-git directory) and the shared local Postgres instance.

- [ ] **Step 1: Confirm the surviving environment works end-to-end before deleting anything**

Verify `soulman-prod`'s six services all start cleanly (Task 5, Step 9 already did this — re-run if any time has passed) and that a real stimulus flows through the pipeline: e.g. run `soulman note "dev environment removal verification"` (or `soulman "..."` — see `cli/NOTES.md`) against the prod perception-svc, and confirm the entry lands in `soulman-prod\reports\daily-report-<today>.txt`.

- [ ] **Step 2: Confirm the schemas to be dropped**

```bash
docker exec supabase_db_agent-suite psql -U postgres -c "\dn" | grep -i "memory\|projects"
```
Expected output includes `memory_dev`, `memory_prod`, `projects_dev`, `projects_prod`, `projects_test`.

- [ ] **Step 3: Drop the dev Postgres schemas**

```bash
docker exec supabase_db_agent-suite psql -U postgres -c "DROP SCHEMA memory_dev CASCADE; DROP SCHEMA projects_dev CASCADE;"
```

- [ ] **Step 4: Verify the drop**

```bash
docker exec supabase_db_agent-suite psql -U postgres -c "\dn" | grep -i "memory\|projects"
```
Expected: `memory_dev` and `projects_dev` no longer appear; `memory_prod`, `projects_prod`, `projects_test` still do.

- [ ] **Step 5: Delete the soulman-dev directory**

```powershell
Remove-Item -Recurse -Force "C:\Users\Lenovo\soulman-dev"
```

- [ ] **Step 6: Verify**

```powershell
Test-Path "C:\Users\Lenovo\soulman-dev"
```
Expected: `False`.

- [ ] **Step 7: Report completion**

No commit for this task (it touches nothing inside this git repo). This is the final step of the plan — the dev environment (code, config, build/deploy scripts, skill, docs, runtime directory, and Postgres schemas) has been fully removed.
