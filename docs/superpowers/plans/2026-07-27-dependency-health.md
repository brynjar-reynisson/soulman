# Dependency Health Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give memory-svc's Postgres connection (and, by pattern, any future dependency) a generic, visible, self-healing health state — distinct from "process alive" — that perception-svc polls and notifies Discord about on transition only.

**Architecture:** A new `common/dephealth` package holds a thread-safe registry of named dependency statuses. memory-svc wraps its Postgres pool in a swappable `DBHolder` that records every real call's outcome into the registry and runs a 30s background reconnect/ping loop; `/health` exposes the registry. perception-svc's `sysmonitor` gets a new `internal_health` check type that polls a service's `/health`, parses its `dependencies` map, and runs each dependency through the existing edge-triggered `publishTransition` state machine already shared by every other check type.

**Tech Stack:** Go 1.25, stdlib only (`log/slog`, `net/http`, `encoding/json`, `sync`) — no new dependencies.

## Global Constraints

- Go 1.25, every service's existing `go.mod` — no new third-party dependencies for this feature.
- Logging follows this repo's established convention (see root `CLAUDE.md`'s Logging section): `slog.Error` for genuine failures, `slog.Warn` for self-healing/expected-fallback, `slog.Info` for routine lifecycle events (e.g. a successful reconnect).
- Notifications are transition-only — no periodic "still down" reminder (approved design decision, see `docs/superpowers/specs/2026-07-27-dependency-health-design.md`'s Non-Goals).
- Reuse perception-svc's existing edge-triggered `publishTransition` state machine (`perception-svc/sysmonitor/sysmonitor.go`) for every internal_health transition — do not build a second dedup mechanism.
- Discord-bound error detail text is truncated to 200 characters, matching the existing convention used elsewhere in this codebase (Gmail channel, Log Error channel).
- This plan's branch is `feature/dependency-health`, already created and pushed with the approved design spec as its first commit.

---

### Task 1: `common/dephealth` registry package

**Files:**
- Create: `common/dephealth/registry.go`
- Test: `common/dephealth/registry_test.go`

**Interfaces:**
- Produces: `dephealth.Status{State string, Since time.Time, Detail string}`, `dephealth.Registry` with `NewRegistry() *Registry`, `(*Registry) Record(name string, err error)`, `(*Registry) Snapshot() map[string]Status`. Every later task imports this as `soulman/common/dephealth`.

- [ ] **Step 1: Write the failing tests**

Create `common/dephealth/registry_test.go`:

```go
package dephealth_test

import (
	"errors"
	"testing"

	"soulman/common/dephealth"
)

func TestRegistry_Record_NilError_SetsOK(t *testing.T) {
	r := dephealth.NewRegistry()
	r.Record("postgres", nil)

	snap := r.Snapshot()
	st, ok := snap["postgres"]
	if !ok {
		t.Fatal(`Snapshot()["postgres"] missing after Record(nil)`)
	}
	if st.State != "ok" {
		t.Errorf("State = %q, want ok", st.State)
	}
	if st.Detail != "" {
		t.Errorf("Detail = %q, want empty", st.Detail)
	}
}

func TestRegistry_Record_Error_SetsDown(t *testing.T) {
	r := dephealth.NewRegistry()
	r.Record("postgres", errors.New("connection refused"))

	st := r.Snapshot()["postgres"]
	if st.State != "down" {
		t.Errorf("State = %q, want down", st.State)
	}
	if st.Detail != "connection refused" {
		t.Errorf("Detail = %q, want %q", st.Detail, "connection refused")
	}
	if st.Since.IsZero() {
		t.Error("Since is zero, want a timestamp")
	}
}

func TestRegistry_Record_RepeatedSameState_DoesNotResetSince(t *testing.T) {
	r := dephealth.NewRegistry()
	r.Record("postgres", errors.New("first failure"))
	firstSince := r.Snapshot()["postgres"].Since

	r.Record("postgres", errors.New("second failure, still down"))
	second := r.Snapshot()["postgres"]

	if !second.Since.Equal(firstSince) {
		t.Errorf("Since changed on repeated down->down Record: got %v, want unchanged %v", second.Since, firstSince)
	}
	if second.Detail != "second failure, still down" {
		t.Errorf("Detail = %q, want the latest error's text (%q)", second.Detail, "second failure, still down")
	}
}

func TestRegistry_Record_Transition_UpdatesSince(t *testing.T) {
	r := dephealth.NewRegistry()
	r.Record("postgres", errors.New("down"))
	downSince := r.Snapshot()["postgres"].Since

	r.Record("postgres", nil)
	okStatus := r.Snapshot()["postgres"]

	if okStatus.State != "ok" {
		t.Fatalf("State = %q, want ok", okStatus.State)
	}
	if okStatus.Since.Equal(downSince) {
		t.Error("Since did not change on a down->ok transition")
	}
}

func TestRegistry_Snapshot_Empty_ReturnsEmptyMap(t *testing.T) {
	r := dephealth.NewRegistry()
	snap := r.Snapshot()
	if len(snap) != 0 {
		t.Errorf("Snapshot() = %v, want empty map", snap)
	}
}

func TestRegistry_Snapshot_MultipleNames_Independent(t *testing.T) {
	r := dephealth.NewRegistry()
	r.Record("postgres", nil)
	r.Record("discord", errors.New("webhook timeout"))

	snap := r.Snapshot()
	if snap["postgres"].State != "ok" {
		t.Errorf("postgres.State = %q, want ok", snap["postgres"].State)
	}
	if snap["discord"].State != "down" {
		t.Errorf("discord.State = %q, want down", snap["discord"].State)
	}
}

func TestRegistry_Snapshot_ReturnsCopy_NotLiveMap(t *testing.T) {
	r := dephealth.NewRegistry()
	r.Record("postgres", nil)

	snap := r.Snapshot()
	snap["postgres"] = dephealth.Status{State: "down"}

	fresh := r.Snapshot()
	if fresh["postgres"].State != "ok" {
		t.Error("mutating a returned Snapshot() map leaked into the registry's internal state")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go -C common test ./dephealth/... -v`
Expected: FAIL — `package soulman/common/dephealth is not in std` / no such package, since `registry.go` doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `common/dephealth/registry.go`:

```go
// Package dephealth tracks whether a service's named dependency (a
// database connection, a webhook, an external API) is currently
// reachable — distinct from whether the service's own process is alive.
// See docs/superpowers/specs/2026-07-27-dependency-health-design.md.
package dephealth

import (
	"sync"
	"time"
)

// Status is one dependency's current state. Since is when the current
// State was entered — it does not reset on a repeated Record call that
// reports the same State, only on an actual transition.
type Status struct {
	State  string // "ok" or "down"
	Since  time.Time
	Detail string // last error's text; empty when State is "ok"
}

// Registry is a thread-safe map of dependency name to its current Status.
type Registry struct {
	mu    sync.Mutex
	items map[string]Status
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{items: make(map[string]Status)}
}

// Record updates name's status: err == nil means "ok", any non-nil err
// means "down" (with Detail set to err.Error()). Safe to call on every
// operation, not only on a real change — Since only advances when the
// State actually flips.
func (r *Registry) Record(name string, err error) {
	state := "ok"
	detail := ""
	if err != nil {
		state = "down"
		detail = err.Error()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	prev, seen := r.items[name]
	since := time.Now().UTC()
	if seen && prev.State == state {
		since = prev.Since
	}
	r.items[name] = Status{State: state, Since: since, Detail: detail}
}

// Snapshot returns a copy of every recorded dependency's current status.
// Safe to call concurrently with Record.
func (r *Registry) Snapshot() map[string]Status {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make(map[string]Status, len(r.items))
	for k, v := range r.items {
		out[k] = v
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go -C common test ./dephealth/... -v`
Expected: PASS — all 7 tests green.

- [ ] **Step 5: Commit**

```bash
git -C "C:/Users/Lenovo/Documents/obsidian/soulman/.claude/worktrees/feature+dependency-health" add common/dephealth/registry.go common/dephealth/registry_test.go
git -C "C:/Users/Lenovo/Documents/obsidian/soulman/.claude/worktrees/feature+dependency-health" commit -m "feat(common): add dephealth registry for tracking dependency status"
```

---

### Task 2: memory-svc storage — DBHolder and Reconnector

**Files:**
- Modify: `memory-svc/storage/postgres.go` (add `Ping` method)
- Create: `memory-svc/storage/dbholder.go`
- Create: `memory-svc/storage/reconnect.go`
- Modify: `memory-svc/storage/writer.go` (field type `*DB` → `*DBHolder`)
- Test: `memory-svc/storage/dbholder_test.go`
- Test: `memory-svc/storage/reconnect_test.go`
- Modify: `memory-svc/storage/writer_test.go` (2 call sites need a `*DBHolder` wrapper instead of a bare `*DB`)

**Interfaces:**
- Consumes: `dephealth.Registry` from Task 1 (`soulman/common/dephealth`); `storage.DB`, `storage.NewDB`, `storage.Episode`, `storage.RawInput` (all pre-existing in this package).
- Produces: `storage.ErrNotConnected` (sentinel error), `storage.DBHolder` with `NewDBHolder(db *DB, registry *dephealth.Registry) *DBHolder`, `(*DBHolder) Get() *DB`, `(*DBHolder) Close()`, `(*DBHolder) InsertRawInput(ctx, *common.Stimulus) error`, `(*DBHolder) GetRecent(ctx, limit int) ([]RawInput, error)`, `(*DBHolder) GetRecentEpisodes(ctx, limit int) ([]Episode, error)`, `(*DBHolder) WriteEpisode(ctx, streamSeq uint64, rec *common.OutcomeRecord) error` — Task 3's httpserver and Task 3's main.go both consume this type. `storage.Reconnector` with `NewReconnector(holder *DBHolder, registry *dephealth.Registry, connStr, schema string) *Reconnector` and `(*Reconnector) Run(ctx context.Context)` — Task 3's main.go starts this as a goroutine.

- [ ] **Step 1: Add `Ping` to `*DB`**

In `memory-svc/storage/postgres.go`, add this method after `NewDB` (after line 37):

```go
// Ping verifies the connection is still alive — used by Reconnector to
// detect a mid-run failure during a quiet period with no write traffic.
func (db *DB) Ping(ctx context.Context) error {
	return db.pool.Ping(ctx)
}
```

- [ ] **Step 2: Write the failing DBHolder tests**

Create `memory-svc/storage/dbholder_test.go`:

```go
package storage_test

import (
	"context"
	"errors"
	"testing"

	"soulman/common/dephealth"
	"soulman/memory-svc/storage"
)

func TestDBHolder_NewDBHolder_NilDB_GetReturnsNil(t *testing.T) {
	reg := dephealth.NewRegistry()
	h := storage.NewDBHolder(nil, reg)

	if h.Get() != nil {
		t.Error("Get() = non-nil, want nil for a holder constructed with nil db")
	}
}

func TestDBHolder_NewDBHolder_NilDB_RecordsDown(t *testing.T) {
	reg := dephealth.NewRegistry()
	storage.NewDBHolder(nil, reg)

	st, ok := reg.Snapshot()["postgres"]
	if !ok {
		t.Fatal(`Snapshot()["postgres"] missing after NewDBHolder(nil, ...)`)
	}
	if st.State != "down" {
		t.Errorf("State = %q, want down", st.State)
	}
}

func TestDBHolder_NewDBHolder_ConnectedDB_RecordsOK(t *testing.T) {
	db := testDB(t) // skips if Postgres unavailable
	reg := dephealth.NewRegistry()
	h := storage.NewDBHolder(db, reg)

	if h.Get() != db {
		t.Error("Get() did not return the db passed to NewDBHolder")
	}
	if reg.Snapshot()["postgres"].State != "ok" {
		t.Errorf("State = %q, want ok", reg.Snapshot()["postgres"].State)
	}
}

func TestDBHolder_InsertRawInput_NotConnected_ReturnsErrNotConnected(t *testing.T) {
	h := storage.NewDBHolder(nil, dephealth.NewRegistry())

	err := h.InsertRawInput(context.Background(), nil)
	if !errors.Is(err, storage.ErrNotConnected) {
		t.Errorf("err = %v, want ErrNotConnected", err)
	}
}

func TestDBHolder_GetRecent_NotConnected_ReturnsErrNotConnected(t *testing.T) {
	h := storage.NewDBHolder(nil, dephealth.NewRegistry())

	_, err := h.GetRecent(context.Background(), 5)
	if !errors.Is(err, storage.ErrNotConnected) {
		t.Errorf("err = %v, want ErrNotConnected", err)
	}
}

func TestDBHolder_GetRecentEpisodes_NotConnected_ReturnsErrNotConnected(t *testing.T) {
	h := storage.NewDBHolder(nil, dephealth.NewRegistry())

	_, err := h.GetRecentEpisodes(context.Background(), 5)
	if !errors.Is(err, storage.ErrNotConnected) {
		t.Errorf("err = %v, want ErrNotConnected", err)
	}
}

func TestDBHolder_WriteEpisode_NotConnected_ReturnsErrNotConnected(t *testing.T) {
	h := storage.NewDBHolder(nil, dephealth.NewRegistry())

	err := h.WriteEpisode(context.Background(), 1, nil)
	if !errors.Is(err, storage.ErrNotConnected) {
		t.Errorf("err = %v, want ErrNotConnected", err)
	}
}

func TestDBHolder_Close_NilDB_DoesNotPanic(t *testing.T) {
	h := storage.NewDBHolder(nil, dephealth.NewRegistry())
	h.Close() // must not panic
}
```

- [ ] **Step 3: Run the DBHolder tests to verify they fail**

Run: `go -C memory-svc test ./storage/... -run TestDBHolder -v`
Expected: FAIL to compile — `storage.NewDBHolder`, `storage.ErrNotConnected` don't exist yet.

- [ ] **Step 4: Implement DBHolder**

Create `memory-svc/storage/dbholder.go`:

```go
package storage

import (
	"context"
	"errors"
	"sync"

	"soulman/common"
	"soulman/common/dephealth"
)

// ErrNotConnected is returned by every DBHolder delegate method when no
// live Postgres connection is currently held. Distinct from a
// query-level error so callers (httpserver's handlers) can tell
// "database unavailable" (503) apart from "query itself failed" (500).
var ErrNotConnected = errors.New("postgres: not connected")

// DBHolder makes the *DB a service holds swappable at runtime — this is
// what lets memory-svc recover from a Postgres outage without a process
// restart (see Reconnector). It is always non-nil once constructed; the
// inner *DB toggles between nil (disconnected) and a live pool. Every
// delegate method records the outcome of each real call into the shared
// dephealth.Registry, so callers never have to remember to do so
// themselves. See docs/superpowers/specs/2026-07-27-dependency-health-design.md.
type DBHolder struct {
	mu       sync.RWMutex
	db       *DB
	registry *dephealth.Registry
}

// NewDBHolder wraps db (nil if the initial connect attempt at startup
// failed) and records its initial state into registry under the
// "postgres" dependency name.
func NewDBHolder(db *DB, registry *dephealth.Registry) *DBHolder {
	h := &DBHolder{db: db, registry: registry}
	if db == nil {
		registry.Record("postgres", ErrNotConnected)
	} else {
		registry.Record("postgres", nil)
	}
	return h
}

// Get returns the currently held *DB, or nil if disconnected. Safe to
// call concurrently with set.
func (h *DBHolder) Get() *DB {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.db
}

// set swaps in a new *DB (or nil) without touching the registry — only
// called from Reconnector (same package), which records the precise
// outcome itself since it knows the exact error to attach.
func (h *DBHolder) set(db *DB) {
	h.mu.Lock()
	h.db = db
	h.mu.Unlock()
}

// Close closes the currently held connection, if any.
func (h *DBHolder) Close() {
	if db := h.Get(); db != nil {
		db.Close()
	}
}

func (h *DBHolder) InsertRawInput(ctx context.Context, s *common.Stimulus) error {
	db := h.Get()
	if db == nil {
		return ErrNotConnected
	}
	err := db.InsertRawInput(ctx, s)
	h.registry.Record("postgres", err)
	return err
}

func (h *DBHolder) GetRecent(ctx context.Context, limit int) ([]RawInput, error) {
	db := h.Get()
	if db == nil {
		return nil, ErrNotConnected
	}
	rows, err := db.GetRecent(ctx, limit)
	h.registry.Record("postgres", err)
	return rows, err
}

func (h *DBHolder) GetRecentEpisodes(ctx context.Context, limit int) ([]Episode, error) {
	db := h.Get()
	if db == nil {
		return nil, ErrNotConnected
	}
	rows, err := db.GetRecentEpisodes(ctx, limit)
	h.registry.Record("postgres", err)
	return rows, err
}

func (h *DBHolder) WriteEpisode(ctx context.Context, streamSeq uint64, rec *common.OutcomeRecord) error {
	db := h.Get()
	if db == nil {
		return ErrNotConnected
	}
	err := db.WriteEpisode(ctx, streamSeq, rec)
	h.registry.Record("postgres", err)
	return err
}
```

- [ ] **Step 5: Run the DBHolder tests to verify they pass**

Run: `go -C memory-svc test ./storage/... -run TestDBHolder -v`
Expected: PASS for the not-connected tests. `TestDBHolder_NewDBHolder_ConnectedDB_RecordsOK` will SKIP if Postgres isn't reachable locally (via `testDB(t)`'s existing skip behavior) — that's expected in this environment, not a failure.

- [ ] **Step 6: Write the failing Reconnector tests**

`reconnect_test.go` needs to be `package storage` (internal/white-box), not `package storage_test` like the rest of this package's tests — mirroring `perception-svc/sysmonitor`'s existing white-box pattern for the same reason: `Reconnector`'s `tick` method and its injectable `newDB` field are unexported, and the tests construct the struct literal directly to inject a fake.

`postgres_test.go`'s existing `testDB` helper is declared in `package storage_test` (a genuinely different Go package from `package storage`, even though they share a directory), so it is not visible from a `package storage` file — a same-named unexported function cannot be shared across that boundary. Rather than exporting it (which would require either renaming every existing `testDB(t)` call site in `postgres_test.go`/`writer_test.go`/`episodes_test.go` to a qualified `storage.XxxDB(t)`, or naming it `TestDB` and colliding with `go test`'s test-function auto-discovery, since `func TestDB(t *testing.T) *DB` matches the `TestXxx` naming convention `go vet` checks against), this task adds a second, small, private copy of the same helper directly in `reconnect_test.go` — a few duplicated lines is the lower-risk choice here over touching three other tests' call sites, consistent with this repo's existing preference for small independent duplication over cross-package plumbing (see root `CLAUDE.md`'s Logging section, which made the same call for `slog` setup).

Create `memory-svc/storage/reconnect_test.go`:

```go
package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"soulman/common/dephealth"
)

// reconnectTestDB is a private duplicate of postgres_test.go's testDB
// helper (package storage_test) — see this task's note on why it isn't
// shared directly. Skips the test if Postgres isn't reachable locally.
func reconnectTestDB(t *testing.T) *DB {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:54322/postgres"
	}
	ctx := context.Background()
	db, err := NewDB(ctx, dbURL, "memory_dev")
	if err != nil {
		t.Skipf("postgres not available (%v) — set DATABASE_URL to run DB tests", err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestReconnector_Tick_Disconnected_SuccessfulReconnect_SwapsIn(t *testing.T) {
	reg := dephealth.NewRegistry()
	holder := NewDBHolder(nil, reg)
	fakeDB := &DB{} // zero-value stand-in; never dereferenced by this test

	rc := &Reconnector{
		holder:   holder,
		registry: reg,
		connStr:  "unused",
		schema:   "unused",
		interval: time.Millisecond,
		newDB: func(ctx context.Context, connStr, schema string) (*DB, error) {
			return fakeDB, nil
		},
	}

	rc.tick(context.Background())

	if holder.Get() != fakeDB {
		t.Error("holder.Get() did not swap in the reconnected DB")
	}
	if reg.Snapshot()["postgres"].State != "ok" {
		t.Errorf("registry state = %q, want ok after successful reconnect", reg.Snapshot()["postgres"].State)
	}
}

func TestReconnector_Tick_Disconnected_FailedReconnect_StaysDown(t *testing.T) {
	reg := dephealth.NewRegistry()
	holder := NewDBHolder(nil, reg)

	rc := &Reconnector{
		holder:   holder,
		registry: reg,
		connStr:  "unused",
		schema:   "unused",
		interval: time.Millisecond,
		newDB: func(ctx context.Context, connStr, schema string) (*DB, error) {
			return nil, errors.New("connection refused")
		},
	}

	rc.tick(context.Background())

	if holder.Get() != nil {
		t.Error("holder.Get() should still be nil after a failed reconnect attempt")
	}
	st := reg.Snapshot()["postgres"]
	if st.State != "down" {
		t.Errorf("registry state = %q, want down", st.State)
	}
	if st.Detail != "connection refused" {
		t.Errorf("registry detail = %q, want %q", st.Detail, "connection refused")
	}
}

func TestReconnector_Tick_Connected_PingSucceeds_StaysUp(t *testing.T) {
	db := reconnectTestDB(t) // skips if Postgres unavailable
	reg := dephealth.NewRegistry()
	holder := NewDBHolder(db, reg)

	rc := &Reconnector{
		holder:   holder,
		registry: reg,
		interval: time.Millisecond,
		newDB:    NewDB,
	}

	rc.tick(context.Background())

	if holder.Get() != db {
		t.Error("holder.Get() changed after a successful ping — should stay the same connection")
	}
	if reg.Snapshot()["postgres"].State != "ok" {
		t.Errorf("state = %q, want ok", reg.Snapshot()["postgres"].State)
	}
}
```

(A fourth case — "connected, ping fails, gets marked down" — would need a way to make a live `*pgxpool.Pool` start failing mid-test, which isn't practical to arrange from a unit test; it's covered instead by this plan's Manual End-to-End Verification section, where a real Postgres outage is induced.)

- [ ] **Step 7: Run the Reconnector tests to verify they fail**

Run: `go -C memory-svc test ./storage/... -v`
Expected: FAIL to compile — `Reconnector` type and `tick` method don't exist yet.

- [ ] **Step 8: Implement Reconnector**

Create `memory-svc/storage/reconnect.go`:

```go
package storage

import (
	"context"
	"log/slog"
	"time"

	"soulman/common/dephealth"
)

// ReconnectInterval is how often Reconnector checks the current
// connection: attempting a fresh connect while disconnected, or pinging
// the existing pool while connected. This is what catches a mid-run
// failure during a quiet period with no write traffic to trigger
// detection otherwise, and what makes a "down" postgres dependency
// recoverable without a manual process restart — before this, a failed
// startup connect left *DB nil for the process's entire lifetime. See
// docs/superpowers/specs/2026-07-27-dependency-health-design.md.
const ReconnectInterval = 30 * time.Second

type Reconnector struct {
	holder   *DBHolder
	registry *dephealth.Registry
	connStr  string
	schema   string
	interval time.Duration
	newDB    func(ctx context.Context, connStr, schema string) (*DB, error)
}

// NewReconnector builds a Reconnector using the real NewDB and
// ReconnectInterval. Tests construct the struct literal directly (same
// package) to inject a fake newDB and a short interval instead.
func NewReconnector(holder *DBHolder, registry *dephealth.Registry, connStr, schema string) *Reconnector {
	return &Reconnector{
		holder:   holder,
		registry: registry,
		connStr:  connStr,
		schema:   schema,
		interval: ReconnectInterval,
		newDB:    NewDB,
	}
}

// Run blocks until ctx is cancelled, ticking every rc.interval. Call as
// `go reconnector.Run(ctx)`.
func (rc *Reconnector) Run(ctx context.Context) {
	ticker := time.NewTicker(rc.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rc.tick(ctx)
		}
	}
}

func (rc *Reconnector) tick(ctx context.Context) {
	db := rc.holder.Get()

	if db == nil {
		newDB, err := rc.newDB(ctx, rc.connStr, rc.schema)
		if err != nil {
			rc.registry.Record("postgres", err)
			return
		}
		rc.holder.set(newDB)
		rc.registry.Record("postgres", nil)
		slog.Info("storage: postgres reconnected")
		return
	}

	if err := db.Ping(ctx); err != nil {
		db.Close()
		rc.holder.set(nil)
		rc.registry.Record("postgres", err)
		slog.Error("storage: postgres ping failed, marked down", "error", err)
	}
}
```

- [ ] **Step 9: Run all storage package tests to verify they pass**

Run: `go -C memory-svc test ./storage/... -v`
Expected: PASS (DB-requiring tests SKIP if Postgres is unreachable in this environment — expected, not a failure).

- [ ] **Step 10: Update Writer to use DBHolder**

In `memory-svc/storage/writer.go`, change the `db` field's type and both nil-checks (the constructor signature itself does not need to change — `nil` remains a valid `*DBHolder` value, so `TestWriter_Write_FileOnly_WhenDBNil`'s existing `storage.NewWriter(fl, nil)` call keeps compiling and passing unchanged):

```go
type Writer struct {
	fl *FileLog
	db *DBHolder
}

func NewWriter(fl *FileLog, db *DBHolder) *Writer {
	return &Writer{fl: fl, db: db}
}
```

In `Write` (replace the `if w.db == nil` check):

```go
	if w.db == nil || w.db.Get() == nil {
		slog.Warn("writer: DB unavailable, written to file only", "stimulus_id", s.StimulusID)
		return nil
	}
```

In `ReplayPending` (replace its `if w.db == nil` check the same way):

```go
	if w.db == nil || w.db.Get() == nil {
		return nil
	}
```

The rest of both functions (the `w.db.InsertRawInput(ctx, s)` calls) needs no change — `DBHolder.InsertRawInput` has the same signature as `DB.InsertRawInput` and now also records into the registry internally.

- [ ] **Step 11: Update writer_test.go's two DB-backed call sites**

In `memory-svc/storage/writer_test.go`, add the import:

```go
	"soulman/common/dephealth"
```

Change line 43 (`TestWriter_Write_MarkedSynced_WhenDBSucceeds`):

```go
	w := storage.NewWriter(fl, storage.NewDBHolder(db, dephealth.NewRegistry()))
```

Change line 93 (`TestWriter_ReplayPending`):

```go
	w := storage.NewWriter(fl, storage.NewDBHolder(db, dephealth.NewRegistry()))
```

`TestWriter_Write_FileOnly_WhenDBNil` (line 21, `storage.NewWriter(fl, nil)`) needs no change.

- [ ] **Step 12: Run the full storage package test suite**

Run: `go -C memory-svc build ./... && go -C memory-svc test ./storage/... -v`
Expected: builds clean, all tests PASS or SKIP (Postgres-dependent ones).

- [ ] **Step 13: Commit**

```bash
git -C "C:/Users/Lenovo/Documents/obsidian/soulman/.claude/worktrees/feature+dependency-health" add memory-svc/storage/
git -C "C:/Users/Lenovo/Documents/obsidian/soulman/.claude/worktrees/feature+dependency-health" commit -m "feat(memory-svc): add DBHolder + Reconnector for self-healing Postgres health"
```

---

### Task 3: memory-svc wiring — `/health`, HTTP handlers, main.go

**Files:**
- Modify: `memory-svc/httpserver/server.go`
- Modify: `memory-svc/httpserver/server_test.go` (full rewrite — signature and response shape both changed)
- Modify: `memory-svc/main.go`

**Interfaces:**
- Consumes: `storage.DBHolder`, `storage.ErrNotConnected`, `storage.NewReconnector` (Task 2); `dephealth.Registry`, `dephealth.NewRegistry` (Task 1); `natsconsumer.NewMemoryWriteConsumer`'s existing `EpisodeWriter` interface (pre-existing, satisfied automatically by `*storage.DBHolder` since it already has a matching `WriteEpisode` method from Task 2 — no changes needed in `natsconsumer`).
- Produces: `httpserver.New(db *storage.DBHolder, registry *dephealth.Registry, port string) *Server` — the new constructor signature every caller (main.go, tests) must use.

- [ ] **Step 1: Write the failing httpserver tests (full replacement of server_test.go)**

Replace the entire contents of `memory-svc/httpserver/server_test.go`:

```go
package httpserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/common/dephealth"
	"soulman/memory-svc/httpserver"
	"soulman/memory-svc/storage"
)

// newTestServer builds a Server whose db is always disconnected (a nil
// *storage.DB wrapped in its own throwaway registry) — none of these
// tests exercise a real Postgres call, so what matters is reg, the
// registry actually passed to New, which independently drives /health.
func newTestServer(reg *dephealth.Registry) *httpserver.Server {
	return httpserver.New(storage.NewDBHolder(nil, dephealth.NewRegistry()), reg, "9002")
}

func TestHealth_AllDependenciesOK(t *testing.T) {
	reg := dephealth.NewRegistry()
	reg.Record("postgres", nil)
	srv := newTestServer(reg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Status       string                    `json:"status"`
		Dependencies map[string]map[string]any `json:"dependencies"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.Dependencies["postgres"]["status"] != "ok" {
		t.Errorf(`dependencies.postgres.status = %v, want "ok"`, body.Dependencies["postgres"]["status"])
	}
}

func TestHealth_DependencyDown_ReportsDegraded(t *testing.T) {
	reg := dephealth.NewRegistry()
	reg.Record("postgres", errors.New("dial tcp 127.0.0.1:54322: connect: connection refused"))
	srv := newTestServer(reg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Status       string                    `json:"status"`
		Dependencies map[string]map[string]any `json:"dependencies"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "degraded" {
		t.Errorf("status = %q, want degraded", body.Status)
	}
	dep := body.Dependencies["postgres"]
	if dep["status"] != "down" {
		t.Errorf(`dependencies.postgres.status = %v, want "down"`, dep["status"])
	}
	if dep["since"] == nil || dep["since"] == "" {
		t.Error("dependencies.postgres.since missing, want a timestamp")
	}
	if dep["detail"] == nil || dep["detail"] == "" {
		t.Error("dependencies.postgres.detail missing, want the error text")
	}
}

func TestRawInputsRecent_NotConnected_Returns503(t *testing.T) {
	srv := newTestServer(dephealth.NewRegistry())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/raw-inputs/recent", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestMemoryStubs_Return501(t *testing.T) {
	srv := newTestServer(dephealth.NewRegistry())
	paths := []string{"/memory/search", "/memory/procedures", "/memory/goals"}

	for _, path := range paths {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, want 501", path, rec.Code)
		}
	}
}

func TestMemoryEpisodes_NotConnected_Returns503(t *testing.T) {
	srv := newTestServer(dephealth.NewRegistry())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/memory/episodes", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestRawInputsRecent_DefaultLimit(t *testing.T) {
	srv := newTestServer(dephealth.NewRegistry())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/raw-inputs/recent?limit=abc", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusBadRequest {
		t.Error("bad limit param should be silently ignored, not return 400")
	}
}

func TestMemoryEpisodes_DefaultLimit(t *testing.T) {
	srv := newTestServer(dephealth.NewRegistry())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/memory/episodes?limit=abc", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusBadRequest {
		t.Error("bad limit param should be silently ignored, not return 400")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go -C memory-svc test ./httpserver/... -v`
Expected: FAIL to compile — `httpserver.New` still has the old 2-arg signature, `storage.NewDBHolder` isn't imported/used yet in this package.

- [ ] **Step 3: Rewrite server.go**

Replace the entire contents of `memory-svc/httpserver/server.go`:

```go
package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"soulman/common/dephealth"
	"soulman/memory-svc/storage"
)

type Server struct {
	db       *storage.DBHolder
	registry *dephealth.Registry
	port     string
	router   chi.Router
}

func New(db *storage.DBHolder, registry *dephealth.Registry, port string) *Server {
	s := &Server{db: db, registry: registry, port: port}
	s.router = s.buildRouter()
	return s
}

func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) Start() error {
	return http.ListenAndServe(":"+s.port, s.router)
}

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", s.health)
	r.Get("/raw-inputs/recent", s.rawInputsRecent)
	r.Get("/memory/search", stub)
	r.Get("/memory/episodes", s.memoryEpisodes)
	r.Get("/memory/procedures", stub)
	r.Get("/memory/goals", stub)

	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	deps := s.registry.Snapshot()
	status := "ok"
	depsBody := make(map[string]map[string]any, len(deps))
	for name, st := range deps {
		entry := map[string]any{"status": st.State}
		if st.State == "down" {
			status = "degraded"
			entry["since"] = st.Since.UTC().Format(time.RFC3339)
			entry["detail"] = st.Detail
		}
		depsBody[name] = entry
	}

	body := map[string]any{"status": status, "dependencies": depsBody}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

func (s *Server) rawInputsRecent(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	rows, err := s.db.GetRecent(r.Context(), limit)
	if errors.Is(err, storage.ErrNotConnected) {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if rows == nil {
		rows = []storage.RawInput{}
	}
	json.NewEncoder(w).Encode(rows)
}

func (s *Server) memoryEpisodes(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	rows, err := s.db.GetRecentEpisodes(r.Context(), limit)
	if errors.Is(err, storage.ErrNotConnected) {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if rows == nil {
		rows = []storage.Episode{}
	}
	json.NewEncoder(w).Encode(rows)
}

func stub(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Not Implemented", http.StatusNotImplemented)
}
```

- [ ] **Step 4: Run the httpserver tests to verify they pass**

Run: `go -C memory-svc test ./httpserver/... -v`
Expected: PASS — all tests green.

- [ ] **Step 5: Wire everything into main.go**

Replace the entire contents of `memory-svc/main.go`:

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"soulman/common/dephealth"
	"soulman/memory-svc/config"
	"soulman/memory-svc/httpserver"
	"soulman/memory-svc/natsconsumer"
	"soulman/memory-svc/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// File log — must succeed; no file log = no durability guarantee
	fl, err := storage.NewFileLog(cfg.LogDir, storage.DefaultMaxFileSize)
	if err != nil {
		slog.Error("filelog init failed", "error", err)
		os.Exit(1)
	}
	defer fl.Close()

	registry := dephealth.NewRegistry()

	// Postgres — non-fatal; service starts and writes to file when DB is
	// down. dbHolder makes this recoverable: Reconnector retries in the
	// background, and every dependent (Writer, HTTP handlers, the
	// episodes consumer) reads through dbHolder so a later reconnect
	// takes effect without a restart.
	db, dbErr := storage.NewDB(ctx, cfg.DatabaseURL, cfg.Schema)
	if dbErr != nil {
		slog.Warn("postgres unavailable — writes go to file only until DB reconnects", "error", dbErr)
	}
	dbHolder := storage.NewDBHolder(db, registry)
	defer dbHolder.Close()

	reconnector := storage.NewReconnector(dbHolder, registry, cfg.DatabaseURL, cfg.Schema)
	go reconnector.Run(ctx)

	// Writer orchestrates file + DB writes
	w := storage.NewWriter(fl, dbHolder)

	// Replay any file entries that never made it to the DB
	if err := w.ReplayPending(ctx); err != nil {
		slog.Error("replay of pending file entries failed", "error", err)
	}

	// STIMULUS consumer
	cons, err := natsconsumer.New(cfg.NATSURL, cfg.ConsumerName, cfg.StimulusSubject, w)
	if err != nil {
		slog.Error("nats consumer init failed", "error", err)
		os.Exit(1)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		slog.Error("nats consumer start failed", "error", err)
		os.Exit(1)
	}

	// MEMORY_WRITE (episodes) consumer — wired independently of the STIMULUS
	// consumer above, so a hiccup in one never silently disables the other
	// (the "keep dual consumer setup independent" lesson documented in
	// action-svc/NOTES.md). dbHolder may be disconnected; its WriteEpisode
	// returns ErrNotConnected in that case, and NATS NAKs and retries later.
	episodeCons, err := natsconsumer.NewMemoryWriteConsumer(cfg.NATSURL, cfg.EpisodesConsumerName, cfg.MemoryWriteSubject, dbHolder)
	if err != nil {
		slog.Error("nats memory-write consumer init failed", "error", err)
		os.Exit(1)
	}
	defer episodeCons.Close()

	if err := episodeCons.Start(ctx); err != nil {
		slog.Error("nats memory-write consumer start failed", "error", err)
		os.Exit(1)
	}

	// HTTP server (non-blocking)
	srv := httpserver.New(dbHolder, registry, cfg.HTTPPort)
	slog.Info("http listening", "port", cfg.HTTPPort)
	go func() {
		if err := srv.Start(); err != nil {
			slog.Error("http server failed", "error", err)
		}
	}()

	slog.Info("memory-svc started",
		"nats_url", cfg.NATSURL, "db_connected", dbErr == nil, "http_port", cfg.HTTPPort, "log_dir", cfg.LogDir)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("memory-svc shutting down")
}
```

- [ ] **Step 6: Build and test the whole service**

Run: `go -C memory-svc build ./... && go -C memory-svc test ./... -v`
Expected: builds clean, all tests PASS or SKIP (Postgres-dependent ones).

- [ ] **Step 7: Commit**

```bash
git -C "C:/Users/Lenovo/Documents/obsidian/soulman/.claude/worktrees/feature+dependency-health" add memory-svc/httpserver/ memory-svc/main.go
git -C "C:/Users/Lenovo/Documents/obsidian/soulman/.claude/worktrees/feature+dependency-health" commit -m "feat(memory-svc): expose dependency health via /health, wire DBHolder end-to-end"
```

---

### Task 4: perception-svc — `internal_health` check type

**Files:**
- Create: `perception-svc/sysmonitor/internalhealth.go`
- Test: `perception-svc/sysmonitor/internalhealth_test.go`
- Modify: `perception-svc/sysmonitor/sysmonitor.go`
- Modify: `perception-svc/sysmonitor/sysmonitor_test.go` (append new tests only — no existing test call sites change, since `newWatcher`'s 5-arg signature is unchanged)
- Modify: `perception-svc/config/config.go`
- Modify: `perception-svc/config/config_test.go` (append new tests only)

**Interfaces:**
- Consumes: nothing from Tasks 1-3 — this task polls memory-svc's `/health` over HTTP, it does not import memory-svc or common/dephealth directly (the JSON shape is duck-typed via `internalHealthBody`, matching Task 3's `/health` response format: `{"status": "...", "dependencies": {"<name>": {"status": "...", "since": "...", "detail": "..."}}}`).
- Produces: `internal_health` as a valid `CheckConfig.Type` value; no new exported symbols consumed elsewhere (this is a self-contained check type inside the `sysmonitor` package, same as `service_health`).

- [ ] **Step 1: Write the failing internalhealth.go checker tests**

Create `perception-svc/sysmonitor/internalhealth_test.go`:

```go
package sysmonitor

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPInternalHealthChecker_Healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","dependencies":{"postgres":{"status":"ok"}}}`))
	}))
	defer srv.Close()

	body, err := (httpInternalHealthChecker{}).FetchHealth(srv.URL, time.Second)
	if err != nil {
		t.Fatalf("FetchHealth: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("Status = %q, want ok", body.Status)
	}
	if body.Dependencies["postgres"].Status != "ok" {
		t.Errorf("Dependencies[postgres].Status = %q, want ok", body.Dependencies["postgres"].Status)
	}
}

func TestHTTPInternalHealthChecker_DegradedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"degraded","dependencies":{"postgres":{"status":"down","since":"2026-07-27T14:32:00Z","detail":"connection refused"}}}`))
	}))
	defer srv.Close()

	body, err := (httpInternalHealthChecker{}).FetchHealth(srv.URL, time.Second)
	if err != nil {
		t.Fatalf("FetchHealth: %v", err)
	}
	dep := body.Dependencies["postgres"]
	if dep.Status != "down" {
		t.Errorf("Dependencies[postgres].Status = %q, want down", dep.Status)
	}
	if dep.Detail != "connection refused" {
		t.Errorf("Dependencies[postgres].Detail = %q, want %q", dep.Detail, "connection refused")
	}
}

func TestHTTPInternalHealthChecker_UnhealthyStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, err := (httpInternalHealthChecker{}).FetchHealth(srv.URL, time.Second)
	if err == nil {
		t.Fatal("FetchHealth: want error for a 503 response, got nil")
	}
}

func TestHTTPInternalHealthChecker_Unreachable(t *testing.T) {
	// Nothing listens on this port: 127.0.0.1:1 is a reserved low port
	// that refuses connections immediately rather than timing out.
	_, err := (httpInternalHealthChecker{}).FetchHealth("http://127.0.0.1:1/health", time.Second)
	if err == nil {
		t.Fatal("FetchHealth: want error for an unreachable target, got nil")
	}
}

func TestHTTPInternalHealthChecker_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	_, err := (httpInternalHealthChecker{}).FetchHealth(srv.URL, time.Second)
	if err == nil {
		t.Fatal("FetchHealth: want error for invalid JSON body, got nil")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go -C perception-svc test ./sysmonitor/... -run TestHTTPInternalHealthChecker -v`
Expected: FAIL to compile — `httpInternalHealthChecker` doesn't exist yet.

- [ ] **Step 3: Implement the checker**

Create `perception-svc/sysmonitor/internalhealth.go`:

```go
package sysmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// internalHealthDependency mirrors one entry of a soulman service's
// /health response body's "dependencies" map. See
// docs/superpowers/specs/2026-07-27-dependency-health-design.md.
type internalHealthDependency struct {
	Status string `json:"status"`
	Since  string `json:"since,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// internalHealthBody mirrors a soulman service's /health response shape.
type internalHealthBody struct {
	Status       string                              `json:"status"`
	Dependencies map[string]internalHealthDependency `json:"dependencies"`
}

// internalHealthChecker is the seam between runInternalHealthCheck and
// the actual HTTP GET — mirrors healthChecker's separation for
// service_health. Tests inject a fake; httpInternalHealthChecker is the
// real implementation.
type internalHealthChecker interface {
	FetchHealth(target string, timeout time.Duration) (*internalHealthBody, error)
}

type httpInternalHealthChecker struct{}

func (httpInternalHealthChecker) FetchHealth(target string, timeout time.Duration) (*internalHealthBody, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var body internalHealthBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &body, nil
}
```

- [ ] **Step 4: Run the checker tests to verify they pass**

Run: `go -C perception-svc test ./sysmonitor/... -run TestHTTPInternalHealthChecker -v`
Expected: PASS — all 5 tests green.

- [ ] **Step 5: Write the failing Watcher-level tests**

Append to `perception-svc/sysmonitor/sysmonitor_test.go` (add to the end of the file; add `"errors"` and `"sync"` to its existing imports if not already present — `sync` is already imported per the file's existing `fakeStats`/`fakePublisher` types):

```go
type fakeInternalHealth struct {
	mu     sync.Mutex
	bodies map[string]*internalHealthBody
	errs   map[string]error
}

func (f *fakeInternalHealth) FetchHealth(target string, timeout time.Duration) (*internalHealthBody, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.errs[target]; ok {
		return nil, err
	}
	return f.bodies[target], nil
}

func (f *fakeInternalHealth) setBody(target string, body *internalHealthBody) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bodies == nil {
		f.bodies = map[string]*internalHealthBody{}
	}
	f.bodies[target] = body
}

func (f *fakeInternalHealth) setErr(target string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errs == nil {
		f.errs = map[string]error{}
	}
	f.errs[target] = err
}

func internalHealthCheck(name, target string) CheckConfig {
	return CheckConfig{Type: "internal_health", Name: name, Target: target}
}

func TestInternalHealth_Unreachable_PublishesCritical(t *testing.T) {
	pub := &fakePublisher{}
	internal := &fakeInternalHealth{}
	internal.setErr("http://memory-svc/health", errors.New("connection refused"))

	w := newWatcher(&fakeStats{}, nil, []CheckConfig{internalHealthCheck("memory-svc", "http://memory-svc/health")}, pub, time.Hour)
	w.internalHealth = internal

	w.poll(context.Background())

	if pub.publishedCount() != 1 {
		t.Fatalf("published = %d, want 1", pub.publishedCount())
	}
}

func TestInternalHealth_DependencyDown_PublishesCritical(t *testing.T) {
	pub := &fakePublisher{}
	internal := &fakeInternalHealth{}
	internal.setBody("http://memory-svc/health", &internalHealthBody{
		Status:       "degraded",
		Dependencies: map[string]internalHealthDependency{"postgres": {Status: "down", Detail: "connection refused"}},
	})

	w := newWatcher(&fakeStats{}, nil, []CheckConfig{internalHealthCheck("memory-svc", "http://memory-svc/health")}, pub, time.Hour)
	w.internalHealth = internal

	w.poll(context.Background())

	// The reachable service-level baseline (first sighting, ok) is
	// suppressed by publishTransition; only postgres going critical
	// publishes.
	if pub.publishedCount() != 1 {
		t.Fatalf("published = %d, want 1", pub.publishedCount())
	}
}

func TestInternalHealth_DependencyRecovers_PublishesRecovery(t *testing.T) {
	pub := &fakePublisher{}
	internal := &fakeInternalHealth{}
	internal.setBody("http://memory-svc/health", &internalHealthBody{
		Status:       "degraded",
		Dependencies: map[string]internalHealthDependency{"postgres": {Status: "down", Detail: "connection refused"}},
	})

	w := newWatcher(&fakeStats{}, nil, []CheckConfig{internalHealthCheck("memory-svc", "http://memory-svc/health")}, pub, time.Hour)
	w.internalHealth = internal

	w.poll(context.Background()) // postgres down: 1 publish

	internal.setBody("http://memory-svc/health", &internalHealthBody{
		Status:       "ok",
		Dependencies: map[string]internalHealthDependency{"postgres": {Status: "ok"}},
	})
	w.poll(context.Background()) // postgres recovers: 1 more publish

	if pub.publishedCount() != 2 {
		t.Fatalf("published = %d, want 2 (down + recovered)", pub.publishedCount())
	}
}

func TestInternalHealth_SteadyState_NoRepeatPublish(t *testing.T) {
	pub := &fakePublisher{}
	internal := &fakeInternalHealth{}
	internal.setBody("http://memory-svc/health", &internalHealthBody{
		Status:       "degraded",
		Dependencies: map[string]internalHealthDependency{"postgres": {Status: "down", Detail: "connection refused"}},
	})

	w := newWatcher(&fakeStats{}, nil, []CheckConfig{internalHealthCheck("memory-svc", "http://memory-svc/health")}, pub, time.Hour)
	w.internalHealth = internal

	w.poll(context.Background())
	w.poll(context.Background())
	w.poll(context.Background())

	if pub.publishedCount() != 1 {
		t.Fatalf("published = %d, want 1 (no repeat while steady)", pub.publishedCount())
	}
}

func TestInternalHealth_ProcessUnreachable_And_DependencyDown_AreIndependentKeys(t *testing.T) {
	pub := &fakePublisher{}
	internal := &fakeInternalHealth{}
	internal.setErr("http://memory-svc/health", errors.New("connection refused"))

	w := newWatcher(&fakeStats{}, nil, []CheckConfig{internalHealthCheck("memory-svc", "http://memory-svc/health")}, pub, time.Hour)
	w.internalHealth = internal

	w.poll(context.Background()) // unreachable: 1 publish (key: internal_health:memory-svc)

	internal.errs = nil
	internal.setBody("http://memory-svc/health", &internalHealthBody{
		Status:       "degraded",
		Dependencies: map[string]internalHealthDependency{"postgres": {Status: "down", Detail: "still down"}},
	})
	w.poll(context.Background()) // now reachable (recovery publish) + postgres down (new key, first sighting critical, publishes)

	if pub.publishedCount() != 3 {
		t.Fatalf("published = %d, want 3 (unreachable, then process-recovered + postgres-down)", pub.publishedCount())
	}
}
```

- [ ] **Step 6: Run the Watcher-level tests to verify they fail**

Run: `go -C perception-svc test ./sysmonitor/... -run TestInternalHealth -v`
Expected: FAIL to compile — `Watcher.internalHealth` field, `internal_health` case in `runCheck`, and `internalHealthCheck`/`fakeInternalHealth` helper types referencing not-yet-existent behavior don't exist yet.

- [ ] **Step 7: Wire `internal_health` into sysmonitor.go**

In `perception-svc/sysmonitor/sysmonitor.go`:

Add a field to the `Watcher` struct (after `health healthChecker` at line 91):

```go
	internalHealth internalHealthChecker
```

Update `newWatcher` (lines 103-113) to set the new field:

```go
func newWatcher(stats statsProvider, health healthChecker, checks []CheckConfig, publisher Publisher, interval time.Duration) *Watcher {
	return &Watcher{
		checks:         checks,
		stats:          stats,
		health:         health,
		internalHealth: httpInternalHealthChecker{},
		publisher:      publisher,
		interval:       interval,
		state:          map[string]severity{},
		status:         map[string]CheckStatus{},
	}
}
```

Update `checkIdentifier` (lines 176-185) to add a case:

```go
func checkIdentifier(c CheckConfig) string {
	switch c.Type {
	case "disk_space":
		return c.Path
	case "service_health":
		return c.Name
	case "internal_health":
		return c.Name
	default:
		return ""
	}
}
```

Update `runCheck` (lines 223-268) to add a branch right after the existing `service_health` block (after the `return` at line 245, before `value, err := w.measure(c)` at line 248):

```go
	if c.Type == "internal_health" {
		w.runInternalHealthCheck(ctx, c)
		return
	}
```

Add a new method after `runCheck` (after the closing brace at line 268):

```go
// runInternalHealthCheck polls the target soulman service's own /health
// and runs the top-level reachability plus each reported dependency
// through the same edge-triggered publishTransition machinery every
// other check type shares. An unreachable/unparseable endpoint is
// reported once, under the check's own key (mirroring service_health);
// a reachable endpoint's individual dependencies each get their own
// independent transition state (key: "internal_health:<name>:<dep>") so
// "memory-svc process is down" and "memory-svc is up but Postgres is
// down" are never conflated. See
// docs/superpowers/specs/2026-07-27-dependency-health-design.md.
func (w *Watcher) runInternalHealthCheck(ctx context.Context, c CheckConfig) {
	key := checkKey(c)

	body, err := w.internalHealth.FetchHealth(c.Target, serviceHealthTimeout)
	if err != nil {
		detail := err.Error()
		status := CheckStatus{Type: c.Type, Key: c.Name, Severity: string(severityCritical), Detail: detail, CheckedAt: time.Now().UTC()}
		w.recordStatus(key, status)
		w.publishTransition(ctx, key, severityCritical, func() *common.Stimulus {
			return buildServiceHealthStimulus(c, severityCritical, detail)
		})
		return
	}

	okStatus := CheckStatus{Type: c.Type, Key: c.Name, Severity: string(severityOK), CheckedAt: time.Now().UTC()}
	w.recordStatus(key, okStatus)
	w.publishTransition(ctx, key, severityOK, func() *common.Stimulus {
		return buildServiceHealthStimulus(c, severityOK, "")
	})

	depNames := make([]string, 0, len(body.Dependencies))
	for name := range body.Dependencies {
		depNames = append(depNames, name)
	}
	sort.Strings(depNames)

	for _, depName := range depNames {
		dep := body.Dependencies[depName]
		depKey := key + ":" + depName
		sev := severityOK
		if dep.Status == "down" {
			sev = severityCritical
		}
		depStatus := CheckStatus{Type: c.Type, Key: c.Name + ":" + depName, Severity: string(sev), CheckedAt: time.Now().UTC()}
		if sev == severityCritical {
			depStatus.Detail = dep.Detail
		}
		w.recordStatus(depKey, depStatus)
		w.publishTransition(ctx, depKey, sev, func() *common.Stimulus {
			return buildInternalHealthStimulus(c, depName, sev, dep.Detail)
		})
	}
}
```

Add two new functions after `buildServiceHealthStimulus` (after its closing brace, which is at line 438 as read at the start of this task):

```go
// formatInternalHealthMessage mirrors formatServiceHealthMessage but for
// one dependency reported by an internal_health check.
func formatInternalHealthMessage(c CheckConfig, depName string, sev severity, detail string) string {
	if sev == severityOK {
		return fmt.Sprintf("%s: %s recovered", c.Name, depName)
	}
	truncated := detail
	if len(truncated) > 200 {
		truncated = truncated[:200]
	}
	return fmt.Sprintf("%s: %s unavailable: %s", c.Name, depName, truncated)
}

// buildInternalHealthStimulus mirrors buildServiceHealthStimulus but for
// one dependency reported by an internal_health check's /health response.
func buildInternalHealthStimulus(c CheckConfig, depName string, sev severity, detail string) *common.Stimulus {
	now := time.Now().UTC()

	errField := ""
	if sev == severityCritical {
		errField = detail
	}

	specific, _ := json.Marshal(struct {
		CheckType  string `json:"check_type"`
		Name       string `json:"name"`
		Dependency string `json:"dependency"`
		Severity   string `json:"severity"`
		Error      string `json:"error,omitempty"`
	}{
		CheckType:  c.Type,
		Name:       c.Name,
		Dependency: depName,
		Severity:   string(sev),
		Error:      errField,
	})

	msgID := computeMessageID(c.Type, c.Name+":"+depName, sev, now)
	s := newSystemMonitorStimulus(now, c.Type, msgID, formatInternalHealthMessage(c, depName, sev, detail), sev)
	s.ChannelMeta.ChannelSpecific = specific
	return s
}
```

Add `"sort"` to the file's import block if it's not already there (it is — verify against the existing import list at the top of the file before adding a duplicate).

- [ ] **Step 8: Run the sysmonitor package tests to verify they pass**

Run: `go -C perception-svc test ./sysmonitor/... -v`
Expected: PASS — all tests green, including every pre-existing test (their call sites to `newWatcher` are unchanged).

- [ ] **Step 9: Write the failing config validation tests**

Append to `perception-svc/config/config_test.go` (after `TestLoad_ValidServiceHealthCheck_NoThresholdRequired`, before `TestLoad_ZeroLogMonitorReconciliationInterval_ReturnsError`):

```go
func TestLoad_InternalHealthCheckMissingName_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "internal_health", Target: "http://localhost:9002/health"}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for internal_health check with no name, got nil")
	}
}

func TestLoad_InternalHealthCheckMissingTarget_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "internal_health", Name: "memory-svc"}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for internal_health check with no target, got nil")
	}
}

func TestLoad_ValidInternalHealthCheck_NoThresholdRequired(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{
		{Type: "internal_health", Name: "memory-svc", Target: "http://localhost:9002/health"},
	}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon, validLogMonitor)
	os.Setenv("CONFIG_PATH", configPath)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: want no error for a valid internal_health check without thresholds, got %v", err)
	}
	if len(cfg.SystemMonitorChecks) != 1 || cfg.SystemMonitorChecks[0].Target != "http://localhost:9002/health" {
		t.Errorf("SystemMonitorChecks = %+v, want one internal_health check with target http://localhost:9002/health", cfg.SystemMonitorChecks)
	}
}
```

- [ ] **Step 10: Run the config tests to verify they fail**

Run: `go -C perception-svc test ./config/... -run TestLoad_InternalHealth -v`
Expected: FAIL — `internal_health` is currently an "unknown type", so the missing-name/missing-target tests fail (wrong error triggers, but an error is still returned so those two might spuriously pass; the third test, `TestLoad_ValidInternalHealthCheck_NoThresholdRequired`, will FAIL since `internal_health` currently hits the `default` case and returns an "unknown type" error instead of loading successfully).

- [ ] **Step 11: Add `internal_health` validation to config.go**

In `perception-svc/config/config.go`, update the `switch c.Type` block (lines 67-82):

```go
		switch c.Type {
		case "disk_space":
			if c.Path == "" {
				return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (disk_space) has no path configured", configPath, i)
			}
		case "memory", "cpu":
		case "service_health":
			if c.Name == "" {
				return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (service_health) has no name configured", configPath, i)
			}
			if c.Target == "" {
				return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (service_health) has no target configured", configPath, i)
			}
		case "internal_health":
			if c.Name == "" {
				return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (internal_health) has no name configured", configPath, i)
			}
			if c.Target == "" {
				return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (internal_health) has no target configured", configPath, i)
			}
		default:
			return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] has unknown type %q", configPath, i, c.Type)
		}
		if c.Type == "service_health" || c.Type == "internal_health" {
			continue // binary checks: no percent thresholds to validate
		}
```

(This changes line 83's condition from `if c.Type == "service_health" {` to include `internal_health`, and adds the new `case "internal_health":` block — the rest of the function is unchanged.)

- [ ] **Step 12: Run the config tests to verify they pass**

Run: `go -C perception-svc test ./config/... -v`
Expected: PASS — all tests green, including every pre-existing test.

- [ ] **Step 13: Build and test the whole service**

Run: `go -C perception-svc build ./... && go -C perception-svc test ./... -v`
Expected: builds clean, all tests PASS.

- [ ] **Step 14: Commit**

```bash
git -C "C:/Users/Lenovo/Documents/obsidian/soulman/.claude/worktrees/feature+dependency-health" add perception-svc/sysmonitor/ perception-svc/config/
git -C "C:/Users/Lenovo/Documents/obsidian/soulman/.claude/worktrees/feature+dependency-health" commit -m "feat(perception-svc): add internal_health system_monitor check type"
```

---

### Task 5: Config entries and documentation

**Files:**
- Modify: `config/dev.json`
- Modify: `config/prod.json`
- Modify: `memory-svc/NOTES.md`
- Modify: `perception-svc/NOTES.md`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: nothing new — this task only adds config entries the code from Tasks 1-4 already knows how to interpret, plus documentation.
- Produces: nothing consumed by later tasks — this is the final task.

- [ ] **Step 1: Add the `internal_health` check to `config/dev.json`**

In `config/dev.json`, within the `system_monitor.checks` array, add a new entry after the last `service_health` entry (`agent-suite-backend`):

```json
  "system_monitor": {
    "poll_interval_seconds": 300,
    "checks": [
      { "type": "disk_space", "path": "C:\\", "warning_threshold_percent": 80, "critical_threshold_percent": 95 },
      { "type": "memory", "warning_threshold_percent": 85 },
      { "type": "cpu", "warning_threshold_percent": 90 },
      { "type": "service_health", "name": "digital-me-frontend", "target": "http://localhost:5173/" },
      { "type": "service_health", "name": "digital-me-backend", "target": "http://127.0.0.1:8080/actuator/health" },
      { "type": "service_health", "name": "agent-suite-frontend", "target": "https://agent.breynisson.org" },
      { "type": "service_health", "name": "agent-suite-backend", "target": "http://localhost:8091/health" },
      { "type": "internal_health", "name": "memory-svc", "target": "http://localhost:9012/health" }
    ]
  },
```

(`9012` is memory-svc's dev port — prod uses `9002`, dev uses `9012`, per the documented prod-port-plus-10 convention in root `CLAUDE.md`.)

- [ ] **Step 2: Add the `internal_health` check to `config/prod.json`**

In `config/prod.json`, the same array, same insertion point, prod port:

```json
  "system_monitor": {
    "poll_interval_seconds": 300,
    "checks": [
      { "type": "disk_space", "path": "C:\\", "warning_threshold_percent": 80, "critical_threshold_percent": 95 },
      { "type": "memory", "warning_threshold_percent": 85 },
      { "type": "cpu", "warning_threshold_percent": 90 },
      { "type": "service_health", "name": "digital-me-frontend", "target": "http://localhost:5173/" },
      { "type": "service_health", "name": "digital-me-backend", "target": "http://127.0.0.1:8080/actuator/health" },
      { "type": "service_health", "name": "agent-suite-frontend", "target": "https://agent.breynisson.org" },
      { "type": "service_health", "name": "agent-suite-backend", "target": "http://localhost:8091/health" },
      { "type": "internal_health", "name": "memory-svc", "target": "http://localhost:9002/health" }
    ]
  },
```

- [ ] **Step 3: Append a NOTES.md entry for memory-svc**

In `memory-svc/NOTES.md`, append this new section to the end of the file (after the existing "Leveled logging" section, which currently ends the file at line 19):

```markdown

## Dependency health tracking (added 2026-07-27)

Postgres connectivity is tracked via a `common/dephealth.Registry`, wrapped by `storage.DBHolder` — every real Postgres call (insert, query, episode write) records its outcome, and `GET /health` now reports `dependencies.postgres` (`ok`/`down`, plus `since`/`detail` when down) instead of the old flat `db: "connected"/"unavailable"` field. A background `storage.Reconnector` ticks every 30s: while disconnected it retries `NewDB`; while connected it pings the existing pool. This is what makes a Postgres outage self-healing — before this, a failed startup connect left the DB `nil` for the process's entire lifetime, requiring a manual restart to recover even after Postgres came back. See `docs/superpowers/specs/2026-07-27-dependency-health-design.md`.

`perception-svc`'s `system_monitor` polls this service's `/health` via a new `internal_health` check (`config/dev.json`/`config/prod.json`) and notifies Discord on any dependency's `ok`\u2194`down` transition — not on every poll while steady, matching the Log Error channel's dedup philosophy.
```

- [ ] **Step 4: Append a NOTES.md entry for perception-svc**

In `perception-svc/NOTES.md`, append this new section to the end of the file (after the existing "Known deferred issue" section, which currently ends the file at line 52):

```markdown

## `internal_health` check type (added 2026-07-27)

A fifth `system_monitor` check type, alongside `disk_space`/`memory`/`cpu`/`service_health`: polls a *soulman* service's own `GET /health` (currently only `memory-svc`, at `http://localhost:9002/health` prod / `:9012` dev) and parses its `dependencies` map (see `docs/superpowers/specs/2026-07-27-dependency-health-design.md`). Two failure modes are kept as independent transition keys so they're never conflated: the endpoint being unreachable at all (`internal_health:<name>`, reported exactly like `service_health`) versus a specific dependency inside a reachable service being down (`internal_health:<name>:<dependency>`, its own edge-triggered state). Both reuse the exact same `publishTransition` machinery `disk_space`/`memory`/`cpu`/`service_health` already share — no second dedup mechanism was built for this.

This is the generic first instance of a pattern meant to extend to other dependencies later (action-svc's Discord webhook, perception-svc's own Gmail polling, NATS connectivity for any service) — each addition is: instrument that dependency with a `common/dephealth.Registry` call, surface it in that service's `/health`, and (if not already present) add one `internal_health` config entry for that service — one check polls a whole service's `/health` and gets all of its dependencies at once, so a second dependency on an already-checked service needs no new config entry.
```

- [ ] **Step 5: Update root `CLAUDE.md`**

In `CLAUDE.md`, update the `memory-svc` entry (item 1 under Services):

Find:
```
1. **`memory-svc`** — consumes `soulman.stimulus.raw`, writes to `raw_inputs.jsonl` then Postgres (`memory_dev`/`memory_prod` schema). Also durably consumes `action-svc`'s `soulman.memory.write` outcome records into an `episodes` table, exposed read-only via `GET /memory/episodes`; `/memory/search`, `/memory/procedures`, `/memory/goals` remain unimplemented stubs.
   - Specs: `2026-06-27-memory-svc-design.md`, `2026-07-18-memory-episodes-design.md`
   - Notes: `memory-svc/NOTES.md`
```

Replace with:
```
1. **`memory-svc`** — consumes `soulman.stimulus.raw`, writes to `raw_inputs.jsonl` then Postgres (`memory_dev`/`memory_prod` schema). Also durably consumes `action-svc`'s `soulman.memory.write` outcome records into an `episodes` table, exposed read-only via `GET /memory/episodes`; `/memory/search`, `/memory/procedures`, `/memory/goals` remain unimplemented stubs. As of 2026-07-27, its Postgres connection's live reachability is tracked via a `common/dephealth` registry — `GET /health` reports `dependencies.postgres` (`ok`/`down`) — with a 30s background reconnect/ping loop that recovers from an outage without a process restart.
   - Specs: `2026-06-27-memory-svc-design.md`, `2026-07-18-memory-episodes-design.md`, `2026-07-27-dependency-health-design.md`
   - Notes: `memory-svc/NOTES.md`
```

Find, in the `perception-svc` entry (item 2 under Services):
```
2. **`perception-svc`** — normalizes external input into `Stimulus` events on `soulman.stimulus.raw`. Four input channels: **folder-watcher** (`fsnotify` on paths from the shared config file's `watch_paths`), **Gmail** (`gmailwatcher` package — polls the inbox via OAuth2 offline refresh token, dedups via a per-environment Gmail label), **System Monitor** (`sysmonitor` package — polls disk/memory/CPU via `golang.org/x/sys/windows` plus external `service_health` targets via TCP dial/HTTP GET, publishes only on a severity transition), and **Log Error** (`logmonitor` package — tails every sibling service's `*-startup-err.log` for new `slog` `ERROR` lines, publishing once per distinct `(service, msg)` pair per process lifetime). Also serves `POST /api/perceive/cli` (CLI push channel) and `POST /api/perceive/raw` (generic Stimulus injection for debugging).
   - Specs: `2026-07-17-perception-svc-design.md`, `2026-07-18-gmail-channel-design.md`, `2026-07-18-soulman-cli-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-18-system-monitor-channel-design.md`, `2026-07-19-system-monitor-service-health-design.md`, `2026-07-20-system-monitor-dashboard-panel-design.md`, `2026-07-27-log-error-perception-design.md`
   - Notes: `perception-svc/NOTES.md` — real incidents (padded Gmail base64 bodies, a blocking-startup-poll bug, the unbounded-backlog incident that motivated the debugging tools), the Log Error channel's `LOG_DIR` deployment gap
```

Replace with:
```
2. **`perception-svc`** — normalizes external input into `Stimulus` events on `soulman.stimulus.raw`. Four input channels: **folder-watcher** (`fsnotify` on paths from the shared config file's `watch_paths`), **Gmail** (`gmailwatcher` package — polls the inbox via OAuth2 offline refresh token, dedups via a per-environment Gmail label), **System Monitor** (`sysmonitor` package — polls disk/memory/CPU via `golang.org/x/sys/windows`, external `service_health` targets via TCP dial/HTTP GET, and (added 2026-07-27) `internal_health` targets that poll a soulman service's own `/health` and report each of its `dependencies` independently, publishes only on a severity transition), and **Log Error** (`logmonitor` package — tails every sibling service's `*-startup-err.log` for new `slog` `ERROR` lines, publishing once per distinct `(service, msg)` pair per process lifetime). Also serves `POST /api/perceive/cli` (CLI push channel) and `POST /api/perceive/raw` (generic Stimulus injection for debugging).
   - Specs: `2026-07-17-perception-svc-design.md`, `2026-07-18-gmail-channel-design.md`, `2026-07-18-soulman-cli-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-18-system-monitor-channel-design.md`, `2026-07-19-system-monitor-service-health-design.md`, `2026-07-20-system-monitor-dashboard-panel-design.md`, `2026-07-27-log-error-perception-design.md`, `2026-07-27-dependency-health-design.md`
   - Notes: `perception-svc/NOTES.md` — real incidents (padded Gmail base64 bodies, a blocking-startup-poll bug, the unbounded-backlog incident that motivated the debugging tools), the Log Error channel's `LOG_DIR` deployment gap, the `internal_health` check type
```

(The channel count stays "Four" — `internal_health` is a new check type inside the existing System Monitor channel, not a new fifth channel.)

- [ ] **Step 6: Verify everything still builds across the whole repo**

Run:
```bash
go -C common build ./... && go -C common test ./...
go -C memory-svc build ./... && go -C memory-svc test ./...
go -C perception-svc build ./... && go -C perception-svc test ./...
go -C thinking-svc build ./...
go -C action-svc build ./...
go -C web-svc build ./...
go -C cli build ./...
```
Expected: every module builds clean; `thinking-svc`/`action-svc`/`web-svc`/`cli` are untouched by this plan but must still build since `common/dephealth` is a new package in a shared module they all depend on transitively.

- [ ] **Step 7: Commit**

```bash
git -C "C:/Users/Lenovo/Documents/obsidian/soulman/.claude/worktrees/feature+dependency-health" add config/dev.json config/prod.json memory-svc/NOTES.md perception-svc/NOTES.md CLAUDE.md
git -C "C:/Users/Lenovo/Documents/obsidian/soulman/.claude/worktrees/feature+dependency-health" commit -m "docs+config: wire internal_health check into dev/prod, document dependency health"
```

---

## Manual End-to-End Verification (after all tasks complete, before merging)

Not a task with its own commit — a final check before handing off to `superpowers:finishing-a-development-branch`:

1. Rebuild and restart `memory-svc` and `perception-svc` in dev (`soulman-dev`'s launcher scripts).
2. `curl http://localhost:9012/health` (dev's memory-svc port) — confirm the new `dependencies.postgres` shape appears.
3. Stop the local Postgres container (if running) or otherwise make it unreachable.
4. Wait up to ~30s (Reconnector's ping interval) + one `system_monitor` poll interval (dev's `config/dev.json` `poll_interval_seconds`) — confirm `/health` flips to `"status": "degraded"` and a Discord notification arrives in dev's channel.
5. Restart Postgres — confirm `/health` flips back to `"status": "ok"` within ~30s and a "recovered" Discord notification arrives.
6. Confirm memory-svc did **not** need a process restart at any point in steps 3-5 — this is the core behavior change from before this plan.
