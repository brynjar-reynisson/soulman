# System Monitor Dashboard Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a live-ish (last-poll-fresh) System Monitor status view to Soulman's web dashboard, showing disk/memory/CPU and the 4 `service_health` checks, split into "Resources" and "Services" sections.

**Architecture:** `perception-svc/sysmonitor.Watcher` starts tracking a mutex-guarded per-check `CheckStatus` snapshot, updated on every poll (not just on severity transitions, which the existing `state` map still separately owns for publish-gating). A new `GET /api/system-monitor/status` route on `perception-svc` exposes it; `web-svc` proxies it through its existing auth-gated pattern as `GET /api/system-monitor`; the frontend gets a new self-fetching `SystemMonitorPanel` added to the dashboard grid.

**Tech Stack:** Go (`perception-svc`, `web-svc`, each its own module), React + TypeScript + Vitest (`web/`).

## Global Constraints

- Data is last-poll-fresh only — no on-demand/"check now" refresh, no new poll path. The existing 300s poll interval is unchanged.
- `perception-svc`'s new endpoint is unauthenticated, matching every existing `perception-svc` route (`/health`, `/api/perceive/*`) — the auth gate is `web-svc`'s job, one hop later, unchanged from the existing proxy pattern.
- `StatusPanel` (Soulman's own service up/down state via `GET /api/status`) is untouched — this is a separate, new panel.
- `service_health` severity stays binary (`ok`/`critical`, no `warning` tier) in the new status data, matching the existing design.
- Spec of record: `docs/superpowers/specs/2026-07-20-system-monitor-dashboard-panel-design.md`.

---

### Task 1: `perception-svc/sysmonitor` — track per-check status, updated every poll

**Files:**
- Modify: `perception-svc/sysmonitor/sysmonitor.go`
- Test: `perception-svc/sysmonitor/sysmonitor_test.go`

**Interfaces:**
- Produces: `sysmonitor.CheckStatus` (exported struct) and `(*Watcher) Status() []CheckStatus` — Task 2 (`perception-svc/httpserver`) consumes both.

- [ ] **Step 1: Write the failing tests**

Add to `perception-svc/sysmonitor/sysmonitor_test.go` (after the existing `TestPoll_ServiceHealthAndDiskCheck_TrackedIndependently`, before `severityFromStimulus` — or anywhere after the existing tests; exact position doesn't matter, function names are the anchor):

```go
func TestStatus_UpdatesEveryPoll_EvenWithoutTransition(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50}}
	pub := &fakePublisher{}
	w := newWatcher(stats, nil, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)

	w.poll(context.Background())
	first := w.Status()
	if len(first) != 1 {
		t.Fatalf("Status() = %d entries, want 1", len(first))
	}
	if first[0].Type != "disk_space" || first[0].Key != `C:\` {
		t.Errorf("Status()[0] = %+v, want type=disk_space key=C:\\", first[0])
	}
	if first[0].ValuePercent == nil || *first[0].ValuePercent != 50 {
		t.Errorf("ValuePercent = %v, want 50", first[0].ValuePercent)
	}
	if first[0].Severity != "ok" {
		t.Errorf("Severity = %q, want ok", first[0].Severity)
	}
	if first[0].CheckedAt.IsZero() {
		t.Error("CheckedAt must be set")
	}

	stats.mu.Lock()
	stats.disk[`C:\`] = 60 // still ok, no transition — status must still update
	stats.mu.Unlock()
	w.poll(context.Background())

	second := w.Status()
	if second[0].ValuePercent == nil || *second[0].ValuePercent != 60 {
		t.Errorf("ValuePercent after second poll = %v, want 60 (status must update every poll, not just on transition)", second[0].ValuePercent)
	}
}

func TestStatus_ServiceHealth_ReflectsSeverityAndDetail(t *testing.T) {
	health := &fakeHealth{
		healthy: map[string]bool{"svc:1234": false},
		detail:  map[string]string{"svc:1234": "connection refused"},
	}
	pub := &fakePublisher{}
	w := newWatcher(&fakeStats{}, health, []CheckConfig{serviceCheck("svc", "svc:1234")}, pub, time.Hour)

	w.poll(context.Background())

	status := w.Status()
	if len(status) != 1 {
		t.Fatalf("Status() = %d entries, want 1", len(status))
	}
	if status[0].Type != "service_health" || status[0].Key != "svc" {
		t.Errorf("status = %+v, want type=service_health key=svc", status[0])
	}
	if status[0].Severity != "critical" {
		t.Errorf("Severity = %q, want critical", status[0].Severity)
	}
	if status[0].Detail != "connection refused" {
		t.Errorf("Detail = %q, want %q", status[0].Detail, "connection refused")
	}
	if status[0].ValuePercent != nil {
		t.Errorf("ValuePercent = %v, want nil for service_health", status[0].ValuePercent)
	}
}

func TestStatus_ServiceHealth_HealthyHasNoDetail(t *testing.T) {
	health := &fakeHealth{healthy: map[string]bool{"svc:1234": true}}
	pub := &fakePublisher{}
	w := newWatcher(&fakeStats{}, health, []CheckConfig{serviceCheck("svc", "svc:1234")}, pub, time.Hour)

	w.poll(context.Background())

	status := w.Status()
	if status[0].Detail != "" {
		t.Errorf("Detail = %q, want empty when healthy", status[0].Detail)
	}
}

func TestStatus_SortedByKey(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50, `D:\`: 50}}
	pub := &fakePublisher{}
	checks := []CheckConfig{diskCheck(`D:\`), diskCheck(`C:\`)} // intentionally out of order
	w := newWatcher(stats, nil, checks, pub, time.Hour)

	w.poll(context.Background())

	status := w.Status()
	if len(status) != 2 {
		t.Fatalf("Status() = %d entries, want 2", len(status))
	}
	if status[0].Key >= status[1].Key {
		t.Errorf("Status() not sorted: %q before %q", status[0].Key, status[1].Key)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C perception-svc test ./sysmonitor/... -run TestStatus -v`
Expected: FAIL — compile error, `CheckStatus`/`Status` undefined on `Watcher`.

- [ ] **Step 3: Implement**

In `perception-svc/sysmonitor/sysmonitor.go`:

1. Add `"sort"` and `"sync"` to the import block (alongside the existing `"context"`, `"crypto/sha256"`, etc.).

2. Add the new type, near the existing `severity` type definitions:

```go
// CheckStatus is a snapshot of one check's most recent poll result —
// distinct from the internal state map's severity-only tracking, which
// exists purely to gate publish decisions. CheckStatus is updated on every
// poll regardless of whether severity changed, so it always reflects "what
// did the last poll see," not "did anything change."
type CheckStatus struct {
	Type         string    `json:"type"`
	Key          string    `json:"key,omitempty"` // path (disk_space) or name (service_health); absent for memory/cpu
	Severity     string    `json:"severity"`
	ValuePercent *float64  `json:"value_percent,omitempty"` // disk_space/memory/cpu only
	Detail       string    `json:"detail,omitempty"`        // service_health only, set when severity is critical
	CheckedAt    time.Time `json:"checked_at"`
}
```

3. Replace `checkKey` with a refactored version that shares logic with a new `checkIdentifier` helper (behaviorally identical output for every existing case — this only removes duplication, so no test should change behavior):

```go
// checkIdentifier is the check's own identifier within its Type: the disk
// path for disk_space, the service name for service_health, empty for
// memory/cpu (there's only ever one of each).
func checkIdentifier(c CheckConfig) string {
	switch c.Type {
	case "disk_space":
		return c.Path
	case "service_health":
		return c.Name
	default:
		return ""
	}
}

func checkKey(c CheckConfig) string {
	id := checkIdentifier(c)
	if id == "" {
		return c.Type
	}
	return c.Type + ":" + id
}
```

4. Add `mu sync.Mutex` and `status map[string]CheckStatus` fields to `Watcher`, and initialize `status` in `newWatcher`:

```go
type Watcher struct {
	checks    []CheckConfig
	stats     statsProvider
	health    healthChecker
	publisher Publisher
	interval  time.Duration

	state map[string]severity

	mu     sync.Mutex
	status map[string]CheckStatus

	cancel context.CancelFunc
}

func newWatcher(stats statsProvider, health healthChecker, checks []CheckConfig, publisher Publisher, interval time.Duration) *Watcher {
	return &Watcher{
		checks:    checks,
		stats:     stats,
		health:    health,
		publisher: publisher,
		interval:  interval,
		state:     map[string]severity{},
		status:    map[string]CheckStatus{},
	}
}
```

5. Add `recordStatus` and `Status`:

```go
func (w *Watcher) recordStatus(key string, s CheckStatus) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status[key] = s
}

// Status returns a snapshot of every check's most recent poll result,
// sorted by key for a deterministic response. Safe to call concurrently
// with the poll loop (e.g. from an HTTP handler's goroutine).
func (w *Watcher) Status() []CheckStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	keys := make([]string, 0, len(w.status))
	for k := range w.status {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]CheckStatus, 0, len(keys))
	for _, k := range keys {
		result = append(result, w.status[k])
	}
	return result
}
```

6. Replace `runCheck` to record status on every poll, for both the `service_health` branch and the percent-based branch:

```go
// runCheck measures one check, records its CheckStatus (every poll,
// regardless of transition), and hands the severity to publishTransition,
// which owns the edge-triggered state machine shared by every check type.
// service_health bypasses measure/deriveSeverity entirely: its severity is
// binary, derived directly from healthChecker.Check.
func (w *Watcher) runCheck(ctx context.Context, c CheckConfig) {
	key := checkKey(c)

	if c.Type == "service_health" {
		healthy, detail := w.health.Check(c.Target, serviceHealthTimeout)
		sev := severityOK
		if !healthy {
			sev = severityCritical
		}
		status := CheckStatus{
			Type:      c.Type,
			Key:       checkIdentifier(c),
			Severity:  string(sev),
			CheckedAt: time.Now().UTC(),
		}
		if sev == severityCritical {
			status.Detail = detail
		}
		w.recordStatus(key, status)
		w.publishTransition(ctx, key, sev, func() *common.Stimulus {
			return buildServiceHealthStimulus(c, sev, detail)
		})
		return
	}

	value, err := w.measure(c)
	if err != nil {
		if errors.Is(err, errNoCPUBaseline) {
			return
		}
		log.Printf("sysmonitor: check %s failed, skipping this poll: %v", key, err)
		return
	}

	sev := deriveSeverity(value, c.WarningThresholdPercent, c.CriticalThresholdPercent)
	w.recordStatus(key, CheckStatus{
		Type:         c.Type,
		Key:          checkIdentifier(c),
		Severity:     string(sev),
		ValuePercent: &value,
		CheckedAt:    time.Now().UTC(),
	})
	w.publishTransition(ctx, key, sev, func() *common.Stimulus {
		return buildStimulus(c, value, sev)
	})
}
```

Note: `&value` is safe here — `value` is a fresh local variable on each `runCheck` call (one call per check per poll), not a loop variable being captured by reference across iterations.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C perception-svc test ./sysmonitor/... -v`
Expected: PASS — all pre-existing tests (unaffected by this change) plus the 4 new `TestStatus_*` tests.

- [ ] **Step 5: Commit**

```bash
git add perception-svc/sysmonitor/sysmonitor.go perception-svc/sysmonitor/sysmonitor_test.go
git commit -m "perception-svc/sysmonitor: track per-check status snapshot, updated every poll"
```

---

### Task 2: `perception-svc/httpserver` — expose `GET /api/system-monitor/status`

**Files:**
- Modify: `perception-svc/httpserver/server.go`
- Modify: `perception-svc/httpserver/server_test.go`
- Modify: `perception-svc/httpserver/cli_test.go`

**Interfaces:**
- Consumes: `sysmonitor.CheckStatus`, `(*sysmonitor.Watcher).Status` (Task 1).
- Produces: `httpserver.New` gains a 5th parameter, `systemMonitorStatus func() []sysmonitor.CheckStatus` — Task 3 (`perception-svc/main.go`) must pass `sm.Status` (where `sm` is the existing `*sysmonitor.Watcher` variable already in `main.go`) as this new argument, or the build breaks. That breakage is expected and out of scope for this task — Task 3 fixes it immediately after.

- [ ] **Step 1: Write the failing tests**

In `perception-svc/httpserver/server_test.go`, add `"soulman/perception-svc/sysmonitor"` to imports, then add:

```go
func TestSystemMonitorStatus_ReturnsJSONArray(t *testing.T) {
	statusFn := func() []sysmonitor.CheckStatus {
		v := 42.0
		return []sysmonitor.CheckStatus{
			{Type: "disk_space", Key: `C:\`, Severity: "ok", ValuePercent: &v, CheckedAt: time.Now()},
		}
	}
	srv := httpserver.New("9001", nil, nil, nil, statusFn)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/system-monitor/status", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body []sysmonitor.CheckStatus
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 1 || body[0].Type != "disk_space" || body[0].Key != `C:\` {
		t.Errorf("body = %+v, want one disk_space C:\\ entry", body)
	}
}

func TestSystemMonitorStatus_NilFunc_ReturnsEmptyArray(t *testing.T) {
	srv := httpserver.New("9001", nil, nil, nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/system-monitor/status", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "[]\n" {
		t.Errorf("body = %q, want []\\n", rec.Body.String())
	}
}
```

`server_test.go`'s two pre-existing `httpserver.New(...)` calls (`TestHealth_ReportsStatusAndWatchedPaths`, `TestHealth_NilStatusFunc_DefaultsToDisconnected`) each need a trailing `, nil` added for the new 5th parameter — you'll see this as a compile error in Step 2; fix both when you get there.

Also fix the 9 `httpserver.New(...)` call sites in `perception-svc/httpserver/cli_test.go` (lines with `httpserver.New("9001", nil, nil, pub)` or `httpserver.New("9001", nil, nil, &fakePublisher{})`) the same way — append `, nil` as the 5th argument to each. `raw_test.go`'s 6 `httpserver.NewWithPublisher(pub)` call sites do **not** need changes; `NewWithPublisher` is updated internally in Step 3 below to supply `nil` for the new parameter itself.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C perception-svc build ./httpserver/... 2>&1 | head -30`
Expected: compile errors — `too few arguments in call to httpserver.New` at every call site not yet updated, plus `undefined: sysmonitor` in `server_test.go` until the import is added. Fix every call site (the 2 in `server_test.go`, the 9 in `cli_test.go`) by appending `, nil` before moving to Step 3's implementation — the test file changes must compile once Step 3 lands.

- [ ] **Step 3: Implement**

In `perception-svc/httpserver/server.go`:

1. Add `"soulman/perception-svc/sysmonitor"` to imports.

2. Update `Server`, `New`, and `NewWithPublisher`:

```go
type Server struct {
	port                string
	watchedPaths        []string
	natsStatus          func() string
	publisher           Publisher
	systemMonitorStatus func() []sysmonitor.CheckStatus
	router              chi.Router
}

func New(port string, watchedPaths []string, natsStatus func() string, publisher Publisher, systemMonitorStatus func() []sysmonitor.CheckStatus) *Server {
	s := &Server{
		port:                port,
		watchedPaths:        watchedPaths,
		natsStatus:          natsStatus,
		publisher:           publisher,
		systemMonitorStatus: systemMonitorStatus,
	}
	s.router = s.buildRouter()
	return s
}

// NewWithPublisher builds a Server for tests that only exercise
// publisher-dependent handlers (like perceiveRaw) and don't care about
// port/watchedPaths/natsStatus/systemMonitorStatus.
func NewWithPublisher(publisher Publisher) *Server {
	return New("0", nil, nil, publisher, nil)
}
```

3. Add the route in `buildRouter`:

```go
	r.Get("/health", s.health)
	r.Get("/api/system-monitor/status", s.systemMonitorStatusHandler)
	r.Post("/api/perceive/cli", s.perceiveCLI)
	r.Post("/api/perceive/raw", s.perceiveRaw)
```

4. Add the handler:

```go
func (s *Server) systemMonitorStatusHandler(w http.ResponseWriter, r *http.Request) {
	var statuses []sysmonitor.CheckStatus
	if s.systemMonitorStatus != nil {
		statuses = s.systemMonitorStatus()
	}
	if statuses == nil {
		statuses = []sysmonitor.CheckStatus{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statuses)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C perception-svc test ./httpserver/... -v`
Expected: PASS — every pre-existing test in `server_test.go`, `cli_test.go`, `raw_test.go`, plus the 2 new tests.

Note: `go -C perception-svc build ./...` (the whole module) will still fail after this step, because `main.go` hasn't been updated to pass the new 5th argument yet — that's Task 3, not this task. Confirm the failure is scoped to `main.go` only: `go -C perception-svc build ./... 2>&1` should show exactly one error, about `main.go`'s call to `httpserver.New`.

- [ ] **Step 5: Commit**

```bash
git add perception-svc/httpserver/server.go perception-svc/httpserver/server_test.go perception-svc/httpserver/cli_test.go
git commit -m "perception-svc/httpserver: expose GET /api/system-monitor/status"
```

---

### Task 3: `perception-svc/main.go` — wire `sm.Status` through

**Files:**
- Modify: `perception-svc/main.go`

**Interfaces:**
- Consumes: `httpserver.New`'s new 5th parameter (Task 2), `(*sysmonitor.Watcher).Status` (Task 1) via the existing `sm` variable already constructed earlier in `main.go`.

No dedicated test — `main.go` is untested wiring code, consistent with Task 5 of the previous `system-monitor-service-health-checks` plan (same file, same reasoning: a straight passthrough, correctness confirmed by the build and by the other tasks' coverage).

- [ ] **Step 1: Update the httpserver.New call**

In `perception-svc/main.go`, change:

```go
	srv := httpserver.New(cfg.HTTPPort, cfg.WatchPaths, pub.Status, pub)
```

to:

```go
	srv := httpserver.New(cfg.HTTPPort, cfg.WatchPaths, pub.Status, pub, sm.Status)
```

(`sm` is the `*sysmonitor.Watcher` already constructed a few lines earlier in this file — no new variable needed.)

- [ ] **Step 2: Verify the build**

Run: `go -C perception-svc build ./...`
Expected: succeeds with no errors.

- [ ] **Step 3: Commit**

```bash
git add perception-svc/main.go
git commit -m "perception-svc: wire sysmonitor.Watcher.Status into the HTTP status endpoint"
```

---

### Task 4: `web-svc/httpserver` — proxy `GET /api/system-monitor`

**Files:**
- Modify: `web-svc/httpserver/server.go`
- Modify: `web-svc/httpserver/proxy_test.go`

**Interfaces:**
- Consumes: `perception-svc`'s `GET /api/system-monitor/status` (Task 2) — via `s.cfg.PerceptionSvcURL`, a field that already exists on `httpserver.Config` and is already populated from shared config (no config changes needed anywhere in this task).
- Produces: `GET /api/system-monitor` on `web-svc` — Task 5 (`web/src/api.ts`) calls this path.

- [ ] **Step 1: Write the failing tests**

Add to `web-svc/httpserver/proxy_test.go` (after the existing episodes/raw-inputs tests):

```go
func TestAPISystemMonitor_ProxiesPerceptionSvc(t *testing.T) {
	var gotPath string
	perception := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"type":"disk_space","key":"C:\\","severity":"ok","value_percent":42,"checked_at":"2026-07-20T00:00:00Z"}]`))
	}))
	defer perception.Close()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", PerceptionSvcURL: perception.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/system-monitor", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/api/system-monitor/status" {
		t.Errorf("proxied path = %q, want /api/system-monitor/status", gotPath)
	}
}

func TestAPISystemMonitor_PerceptionSvcDown_Returns502(t *testing.T) {
	perception := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	perception.Close() // closed immediately: connection refused, simulating "down"

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", PerceptionSvcURL: perception.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/system-monitor", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestAPISystemMonitor_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/system-monitor", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C web-svc test ./httpserver/... -run TestAPISystemMonitor -v`
Expected: FAIL — `404 page not found` (route doesn't exist yet) instead of the expected status codes.

- [ ] **Step 3: Implement**

In `web-svc/httpserver/server.go`, add one line inside the authenticated route group in `buildRouter`:

```go
	r.Group(func(r chi.Router) {
		r.Use(s.verifier.Middleware)
		r.Get("/api/status", s.apiStatus)
		r.Get("/api/episodes", s.proxyGet(s.cfg.MemorySvcURL, "/memory/episodes"))
		r.Get("/api/raw-inputs/recent", s.proxyGet(s.cfg.MemorySvcURL, "/raw-inputs/recent"))
		r.Get("/api/system-monitor", s.proxyGet(s.cfg.PerceptionSvcURL, "/api/system-monitor/status"))
		r.Get("/api/reports/latest", s.reportsLatest)
		r.Get("/api/reports", s.reportsByDate)
	})
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C web-svc test ./httpserver/... -v`
Expected: PASS — all pre-existing tests plus the 3 new `TestAPISystemMonitor_*` tests.

- [ ] **Step 5: Commit**

```bash
git add web-svc/httpserver/server.go web-svc/httpserver/proxy_test.go
git commit -m "web-svc/httpserver: proxy GET /api/system-monitor to perception-svc"
```

---

### Task 5: `web/src/api.ts` — add the fetch function

**Files:**
- Modify: `web/src/api.ts`

**Interfaces:**
- Consumes: `web-svc`'s `GET /api/system-monitor` (Task 4).
- Produces: `CheckStatus` type and `getSystemMonitorStatus` function — Task 6 (`SystemMonitorPanel.tsx`) imports both.

No dedicated test file — `api.ts`'s existing exports (`getEpisodes`, `getRawInputs`, etc.) have no direct unit tests either (`api.test.ts` exists but tests `getJSON`'s error-handling shape generically, not each individual endpoint function); this task follows that same established pattern. Coverage comes from Task 6's component test, which exercises `getSystemMonitorStatus` through mocking.

- [ ] **Step 1: Add the type and function**

In `web/src/api.ts`, add after the existing `Report` interface (before `const WEB_SVC_URL = ...`):

```ts
export interface CheckStatus {
  type: string;
  key?: string;
  severity: 'ok' | 'warning' | 'critical';
  value_percent?: number;
  detail?: string;
  checked_at: string;
}
```

And after the existing `getReportByDate` export (end of file):

```ts

export const getSystemMonitorStatus = (token: string | null): Promise<CheckStatus[]> =>
  getJSON('/api/system-monitor', token);
```

- [ ] **Step 2: Verify the build/typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no new type errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/api.ts
git commit -m "web: add getSystemMonitorStatus API client function"
```

---

### Task 6: `web/src/components/SystemMonitorPanel.tsx` — the new panel

**Files:**
- Create: `web/src/components/SystemMonitorPanel.tsx`
- Test: `web/src/components/SystemMonitorPanel.test.tsx`

**Interfaces:**
- Consumes: `getSystemMonitorStatus`, `CheckStatus` (Task 5), `getAccessToken` (existing, from `web/src/auth.ts`).
- Produces: `SystemMonitorPanel` component — Task 7 (`Dashboard.tsx`) renders it.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/SystemMonitorPanel.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetSystemMonitorStatus = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getSystemMonitorStatus: (...args: unknown[]) => mockGetSystemMonitorStatus(...args) };
});

beforeEach(() => vi.clearAllMocks());

describe('SystemMonitorPanel', () => {
  it('shows resource and service checks once loaded', async () => {
    mockGetSystemMonitorStatus.mockResolvedValue([
      { type: 'disk_space', key: 'C:\\', severity: 'ok', value_percent: 42, checked_at: '2026-07-20T00:00:00Z' },
      { type: 'service_health', key: 'agent-suite-backend', severity: 'ok', checked_at: '2026-07-20T00:00:00Z' },
    ]);
    const { SystemMonitorPanel } = await import('./SystemMonitorPanel');
    render(<SystemMonitorPanel />);

    expect(await screen.findByText('Disk C:\\')).toBeInTheDocument();
    expect(await screen.findByText('42%')).toBeInTheDocument();
    expect(await screen.findByText('agent-suite-backend')).toBeInTheDocument();
    expect(await screen.findByText('up')).toBeInTheDocument();
  });

  it('shows service detail when down', async () => {
    mockGetSystemMonitorStatus.mockResolvedValue([
      {
        type: 'service_health',
        key: 'agent-suite-backend',
        severity: 'critical',
        detail: 'connection refused',
        checked_at: '2026-07-20T00:00:00Z',
      },
    ]);
    const { SystemMonitorPanel } = await import('./SystemMonitorPanel');
    render(<SystemMonitorPanel />);

    expect(await screen.findByText('down')).toBeInTheDocument();
    expect(await screen.findByText('connection refused')).toBeInTheDocument();
  });

  it('shows an error banner without throwing when the fetch fails', async () => {
    mockGetSystemMonitorStatus.mockRejectedValue(new Error('network error'));
    const { SystemMonitorPanel } = await import('./SystemMonitorPanel');
    render(<SystemMonitorPanel />);

    expect(await screen.findByText(/system monitor status unavailable/i)).toBeInTheDocument();
  });

  it('shows empty-state messages when there are no checks', async () => {
    mockGetSystemMonitorStatus.mockResolvedValue([]);
    const { SystemMonitorPanel } = await import('./SystemMonitorPanel');
    render(<SystemMonitorPanel />);

    expect(await screen.findByText(/no resource checks/i)).toBeInTheDocument();
    expect(await screen.findByText(/no service checks/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/components/SystemMonitorPanel.test.tsx`
Expected: FAIL — `Cannot find module './SystemMonitorPanel'`.

- [ ] **Step 3: Implement**

Create `web/src/components/SystemMonitorPanel.tsx`:

```tsx
import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getSystemMonitorStatus, type CheckStatus } from '../api';

const RESOURCE_TYPES = new Set(['disk_space', 'memory', 'cpu']);

function resourceLabel(c: CheckStatus): string {
  if (c.type === 'disk_space') return `Disk ${c.key ?? ''}`;
  if (c.type === 'memory') return 'Memory';
  if (c.type === 'cpu') return 'CPU';
  return c.type;
}

function severityColor(severity: string): string {
  if (severity === 'critical') return 'text-red-600';
  if (severity === 'warning') return 'text-amber-600';
  return 'text-green-600';
}

export function SystemMonitorPanel() {
  const [checks, setChecks] = useState<CheckStatus[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getSystemMonitorStatus(token);
        if (active) setChecks(data);
      } catch {
        if (active) setError('System monitor status unavailable');
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  const resources = checks?.filter((c) => RESOURCE_TYPES.has(c.type)) ?? [];
  const services = checks?.filter((c) => c.type === 'service_health') ?? [];

  return (
    <div className="rounded border bg-white p-4">
      <h2 className="mb-2 font-medium">System Monitor</h2>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && checks === null && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && checks !== null && (
        <>
          <h3 className="mb-1 mt-2 text-sm font-medium text-gray-600">Resources</h3>
          {resources.length === 0 ? (
            <p className="text-sm text-gray-500">No resource checks</p>
          ) : (
            <ul className="space-y-1">
              {resources.map((c) => (
                <li key={`${c.type}:${c.key ?? ''}`} className="flex justify-between text-sm">
                  <span>{resourceLabel(c)}</span>
                  <span className={severityColor(c.severity)}>
                    {c.value_percent !== undefined ? `${Math.round(c.value_percent)}%` : c.severity}
                  </span>
                </li>
              ))}
            </ul>
          )}
          <h3 className="mb-1 mt-3 text-sm font-medium text-gray-600">Services</h3>
          {services.length === 0 ? (
            <p className="text-sm text-gray-500">No service checks</p>
          ) : (
            <ul className="space-y-1">
              {services.map((c) => (
                <li key={`${c.type}:${c.key ?? ''}`} className="text-sm">
                  <div className="flex justify-between">
                    <span>{c.key}</span>
                    <span className={severityColor(c.severity)}>{c.severity === 'critical' ? 'down' : 'up'}</span>
                  </div>
                  {c.severity === 'critical' && c.detail && (
                    <div className="text-xs text-gray-400">{c.detail}</div>
                  )}
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/components/SystemMonitorPanel.test.tsx`
Expected: PASS — all 4 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/SystemMonitorPanel.tsx web/src/components/SystemMonitorPanel.test.tsx
git commit -m "web: add SystemMonitorPanel showing resource and service check status"
```

---

### Task 7: `web/src/components/Dashboard.tsx` — wire the panel in

**Files:**
- Modify: `web/src/components/Dashboard.tsx`

**Interfaces:**
- Consumes: `SystemMonitorPanel` (Task 6).

- [ ] **Step 1: Add the import and render it**

Replace the full contents of `web/src/components/Dashboard.tsx`:

```tsx
import type { ServiceStatus } from '../api';
import { StatusPanel } from './StatusPanel';
import { EpisodesPanel } from './EpisodesPanel';
import { RawInputsPanel } from './RawInputsPanel';
import { ReportsPanel } from './ReportsPanel';
import { SystemMonitorPanel } from './SystemMonitorPanel';

export function Dashboard({
  initialStatus,
  onSignOut,
}: {
  initialStatus: ServiceStatus | null;
  onSignOut: () => void;
}) {
  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Soulman Dashboard</h1>
        <button onClick={onSignOut} className="text-sm text-gray-500 underline">
          Sign out
        </button>
      </div>
      <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
        <StatusPanel initialStatus={initialStatus} />
        <SystemMonitorPanel />
        <ReportsPanel />
        <EpisodesPanel />
        <RawInputsPanel />
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Run the full frontend test suite**

Run: `cd web && npx vitest run`
Expected: PASS — every existing test plus `SystemMonitorPanel.test.tsx`, no regressions.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/Dashboard.tsx
git commit -m "web: render SystemMonitorPanel on the dashboard"
```

---

### Task 8: Update `perception-svc/NOTES.md`, `web-svc/NOTES.md`, and root `CLAUDE.md`

**Files:**
- Modify: `perception-svc/NOTES.md`
- Modify: `web-svc/NOTES.md`
- Modify: `CLAUDE.md`

**Interfaces:** None (documentation only).

- [ ] **Step 1: Add a paragraph to `perception-svc/NOTES.md`**

In `perception-svc/NOTES.md`, after the "System Monitor channel" section's existing content (after the `service_health` paragraph added by the previous feature), add:

```markdown

`GET /api/system-monitor/status` (added 2026-07-20, see `docs/superpowers/specs/2026-07-20-system-monitor-dashboard-panel-design.md`) exposes each check's most recent poll result — a separate, mutex-guarded `status` map on `Watcher`, distinct from the `state` map that only tracks severity for publish-gating. `status` updates on every poll regardless of whether severity changed, so it always reflects "what did the last poll see," not "did anything change." Unauthenticated, like every other `perception-svc` route — `web-svc` is where the dashboard's auth gate lives.
```

- [ ] **Step 2: Add a paragraph to `web-svc/NOTES.md`**

In `web-svc/NOTES.md`, after the existing "`/api/status` never fails even when a downstream service is down" section, add:

```markdown

## `/api/system-monitor` is a plain proxy, not a status aggregator

Unlike `/api/status` (which independently probes 4 services' `/health` endpoints and reports up/down), `/api/system-monitor` (added 2026-07-20) is a thin passthrough of `perception-svc`'s `GET /api/system-monitor/status` via the existing `proxyGet` helper — same pattern as `/api/episodes`/`/api/raw-inputs/recent`. The data's freshness is entirely `perception-svc`'s: whatever its `sysmonitor.Watcher` last polled (up to 5 minutes stale), not re-checked by `web-svc` on each dashboard load.
```

- [ ] **Step 3: Update `CLAUDE.md`**

In `CLAUDE.md`, find the `perception-svc` bullet's Specs line (ends with `2026-07-19-system-monitor-service-health-design.md`) and append the new spec:

```
- Specs: `2026-07-17-perception-svc-design.md`, `2026-07-18-gmail-channel-design.md`, `2026-07-18-soulman-cli-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-18-system-monitor-channel-design.md`, `2026-07-19-system-monitor-service-health-design.md`, `2026-07-20-system-monitor-dashboard-panel-design.md`
```

Find the `web-svc` bullet's Specs line (currently just `2026-07-19-soulman-web-dashboard-design.md`) and append the same new spec:

```
- Specs: `2026-07-19-soulman-web-dashboard-design.md`, `2026-07-20-system-monitor-dashboard-panel-design.md`
```

Also update the `web-svc` bullet's summary sentence — find:

```
Serves `GET /api/status` (aggregates `/health` from the other four services), `GET /api/episodes` and `GET /api/raw-inputs/recent` (proxy `memory-svc`), and `GET /api/reports/latest` / `GET /api/reports?date=` (reads `$SOULMAN_ROOT/reports/*.txt` directly).
```

Replace with:

```
Serves `GET /api/status` (aggregates `/health` from the other four services), `GET /api/episodes` and `GET /api/raw-inputs/recent` (proxy `memory-svc`), `GET /api/system-monitor` (proxies `perception-svc`'s System Monitor check status), and `GET /api/reports/latest` / `GET /api/reports?date=` (reads `$SOULMAN_ROOT/reports/*.txt` directly).
```

- [ ] **Step 4: Commit**

```bash
git add perception-svc/NOTES.md web-svc/NOTES.md CLAUDE.md
git commit -m "docs: note the new system-monitor status endpoint and dashboard panel"
```

---

## Final Verification

After all 8 tasks:

- [ ] `go -C perception-svc test ./... && go -C web-svc test ./...` — expect all PASS.
- [ ] `go -C perception-svc build ./... && go -C web-svc build ./... && go -C thinking-svc build ./... && go -C action-svc build ./... && go -C memory-svc build ./...` — expect all five services still build.
- [ ] `cd web && npx vitest run && npx tsc --noEmit` — expect all frontend tests PASS and no type errors.
- [ ] Manually start `perception-svc` and `web-svc` against dev config, load the dashboard, and confirm the new "System Monitor" panel renders both Resources and Services sections with real current data.
