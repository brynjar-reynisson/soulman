# System Monitor: Service Health Checks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a binary `service_health` check type to `perception-svc/sysmonitor`'s existing System Monitor channel, so it can alert on four external services (`digital-me-frontend`, `digital-me-backend`, `agent-suite-frontend`, `agent-suite-backend`) going down or recovering, reusing the existing poll loop / edge-triggered state machine / mechanical `thinking-svc` rule.

**Architecture:** `service_health` is a fourth `CheckConfig.Type` alongside `disk_space`/`memory`/`cpu`. It carries `Name` + `Target` instead of thresholds, and its severity is binary (`ok`/`critical`, no `warning` tier) rather than threshold-derived. A target string is polymorphic: `http://`/`https://` prefix → HTTP GET, any 2xx is healthy; otherwise → raw TCP dial. A new `healthChecker` interface (separate from the existing `statsProvider`, which mirrors local OS syscalls) is the seam for this network probe. `runCheck`'s branching and a small shared `publishTransition` helper let the existing edge-triggered state machine (in-memory `state` map, first-poll-quiet-if-ok, retry-on-publish-failure) serve both percent-based and binary checks without duplicating that logic.

**Tech Stack:** Go (each service its own module: `common`, `perception-svc`, `thinking-svc`), standard library `net`/`net/http`/`net/http/httptest` only — no new third-party dependency.

## Global Constraints

- No new `action-svc` code — `service_health` alerts reuse the existing mechanical `append_daily_report_entry` action via `thinking-svc`'s `SystemMonitorRule`, unmodified in shape.
- No new `poll_interval_seconds` config knob — `service_health` checks share the existing 300s `system_monitor.poll_interval_seconds` in both `config/dev.json` and `config/prod.json`.
- No per-check timeout config — the dial/GET timeout is a fixed 5-second constant (`serviceHealthTimeout`) in `perception-svc/sysmonitor`.
- Severity for `service_health` is strictly binary: `ok` or `critical`. No `warning` tier.
- SSL certificate expiry checking is explicitly out of scope — `https://` targets are checked only for a successful 2xx response.
- Spec of record: `docs/superpowers/specs/2026-07-19-system-monitor-service-health-design.md`. Base System Monitor spec (unmodified by this plan): `docs/superpowers/specs/2026-07-18-system-monitor-channel-design.md`.

---

### Task 1: `common/sharedconfig` — add `Name`/`Target` fields to `CheckConfig`

**Files:**
- Modify: `common/sharedconfig/config.go:74-83`
- Test: `common/sharedconfig/config_test.go`

**Interfaces:**
- Produces: `sharedconfig.CheckConfig` gains `Name string` (JSON `name,omitempty`) and `Target string` (JSON `target,omitempty`). Both are `service_health`-only; `perception-svc/config` (Task 2) and `perception-svc/main.go` (Task 5) read them.

- [ ] **Step 1: Write the failing test**

Add to `common/sharedconfig/config_test.go` (after `TestLoad_SystemMonitorFields`):

```go
func TestLoad_SystemMonitorServiceHealthFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"watch_paths": ["C:\\a\\errors"],
		"system_monitor": {
			"poll_interval_seconds": 300,
			"checks": [
				{"type": "service_health", "name": "agent-suite-backend", "target": "http://localhost:8091/health"}
			]
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.SystemMonitor.Checks) != 1 {
		t.Fatalf("SystemMonitor.Checks = %d entries, want 1", len(cfg.SystemMonitor.Checks))
	}
	svc := cfg.SystemMonitor.Checks[0]
	if svc.Type != "service_health" || svc.Name != "agent-suite-backend" || svc.Target != "http://localhost:8091/health" {
		t.Errorf("Checks[0] = %+v, want service_health agent-suite-backend http://localhost:8091/health", svc)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C common test ./sharedconfig/... -run TestLoad_SystemMonitorServiceHealthFields -v`
Expected: FAIL — `svc.Name`/`svc.Target` undefined (compile error, since the fields don't exist yet on `CheckConfig`).

- [ ] **Step 3: Add the fields**

In `common/sharedconfig/config.go`, replace the `CheckConfig` struct (lines 74-83):

```go
// CheckConfig describes one system-monitor check. CriticalThresholdPercent
// is optional — a zero value means this check only ever reports ok/warning,
// never critical. Perception module.md's own example config only gives
// disk_space a critical threshold, leaving memory and cpu warning-only.
// Name and Target are service_health-only: Target is polymorphic, detected
// by prefix ("http://"/"https://" → HTTP GET; otherwise → "host:port" TCP
// dial) — see docs/superpowers/specs/2026-07-19-system-monitor-service-health-design.md.
type CheckConfig struct {
	Type                     string  `json:"type"` // "disk_space" | "memory" | "cpu" | "service_health"
	Path                     string  `json:"path,omitempty"`   // disk_space only
	Name                     string  `json:"name,omitempty"`   // service_health only
	Target                   string  `json:"target,omitempty"` // service_health only
	WarningThresholdPercent  float64 `json:"warning_threshold_percent,omitempty"`
	CriticalThresholdPercent float64 `json:"critical_threshold_percent,omitempty"`
}
```

Note: `warning_threshold_percent`'s JSON tag also picks up `,omitempty` here — harmless for the existing three check types (they always set a positive value, so it's never omitted in practice) and correct for `service_health`, which never sets it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go -C common test ./sharedconfig/... -v`
Expected: PASS (all tests in the package, including the new one and every pre-existing one).

- [ ] **Step 5: Commit**

```bash
git add common/sharedconfig/config.go common/sharedconfig/config_test.go
git commit -m "common/sharedconfig: add Name/Target fields to CheckConfig for service_health"
```

---

### Task 2: `perception-svc/config` — fatal-fast validation for `service_health`

**Files:**
- Modify: `perception-svc/config/config.go:61-77`
- Test: `perception-svc/config/config_test.go`

**Interfaces:**
- Consumes: `sharedconfig.CheckConfig.Name`/`.Target` (Task 1).
- Produces: `config.Load()` returns an error if a `service_health` check has empty `name` or `target`; otherwise unchanged behavior (no threshold validation applied to `service_health` checks).

- [ ] **Step 1: Write the failing tests**

Add to `perception-svc/config/config_test.go`: extend the local `checkFields` test-fixture struct (lines 18-23) with the two new fields, then add three new test functions.

Replace `checkFields` (lines 18-23):

```go
type checkFields struct {
	Type                     string  `json:"type"`
	Path                     string  `json:"path,omitempty"`
	Name                     string  `json:"name,omitempty"`
	Target                   string  `json:"target,omitempty"`
	WarningThresholdPercent  float64 `json:"warning_threshold_percent,omitempty"`
	CriticalThresholdPercent float64 `json:"critical_threshold_percent,omitempty"`
}
```

Add after `TestLoad_ValidMemoryAndCPUChecks_NoPathRequired` (end of file):

```go
func TestLoad_ServiceHealthCheckMissingName_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "service_health", Target: "localhost:5176"}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for service_health check with no name, got nil")
	}
}

func TestLoad_ServiceHealthCheckMissingTarget_ReturnsError(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{{Type: "service_health", Name: "agent-suite-backend"}}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon)
	os.Setenv("CONFIG_PATH", configPath)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load: want error for service_health check with no target, got nil")
	}
}

func TestLoad_ValidServiceHealthCheck_NoThresholdRequired(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	sysMon := validSystemMonitor
	sysMon.Checks = []checkFields{
		{Type: "service_health", Name: "agent-suite-backend", Target: "http://localhost:8091/health"},
		{Type: "service_health", Name: "digital-me-frontend", Target: "localhost:5173"},
	}
	configPath := writeConfigFile(t, []string{`C:\a\errors`}, "nats://localhost:4222", "soulman.stimulus.raw", validGmail, sysMon)
	os.Setenv("CONFIG_PATH", configPath)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: want no error for valid service_health checks without thresholds, got %v", err)
	}
	if len(cfg.SystemMonitorChecks) != 2 {
		t.Fatalf("SystemMonitorChecks = %d entries, want 2", len(cfg.SystemMonitorChecks))
	}
	if cfg.SystemMonitorChecks[0].Name != "agent-suite-backend" || cfg.SystemMonitorChecks[0].Target != "http://localhost:8091/health" {
		t.Errorf("SystemMonitorChecks[0] = %+v, want agent-suite-backend/http://localhost:8091/health", cfg.SystemMonitorChecks[0])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C perception-svc test ./config/... -run TestLoad_ServiceHealth -v`
Expected: `TestLoad_ServiceHealthCheckMissingName_ReturnsError` and `TestLoad_ServiceHealthCheckMissingTarget_ReturnsError` FAIL (no error returned — `service_health` currently falls into the `default:` case and errors on "unknown type", which actually makes those two pass already; but `TestLoad_ValidServiceHealthCheck_NoThresholdRequired` FAILs because `service_health` isn't a recognized type yet, so `Load` returns an "unknown type" error instead of succeeding).

- [ ] **Step 3: Add `service_health` handling to the validation loop**

In `perception-svc/config/config.go`, replace the check-validation loop (lines 61-77):

```go
	for i, c := range shared.SystemMonitor.Checks {
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
		default:
			return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] has unknown type %q", configPath, i, c.Type)
		}
		if c.Type == "service_health" {
			continue // binary check: no percent thresholds to validate
		}
		if c.WarningThresholdPercent <= 0 {
			return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (%s) has no positive warning_threshold_percent configured", configPath, i, c.Type)
		}
		if c.CriticalThresholdPercent > 0 && c.CriticalThresholdPercent < c.WarningThresholdPercent {
			return nil, fmt.Errorf("shared config %s: system_monitor.checks[%d] (%s) has critical_threshold_percent below warning_threshold_percent", configPath, i, c.Type)
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C perception-svc test ./config/... -v`
Expected: PASS (all tests in the package).

- [ ] **Step 5: Commit**

```bash
git add perception-svc/config/config.go perception-svc/config/config_test.go
git commit -m "perception-svc/config: validate service_health checks (name+target required, no thresholds)"
```

---

### Task 3: `perception-svc/sysmonitor` — `healthChecker` seam + real TCP/HTTP implementation

**Files:**
- Create: `perception-svc/sysmonitor/servicehealth.go`
- Test: `perception-svc/sysmonitor/servicehealth_test.go`

**Interfaces:**
- Produces: `healthChecker` interface (`Check(target string, timeout time.Duration) (healthy bool, detail string)`) and `httpTCPHealthChecker` (its real implementation), plus the `serviceHealthTimeout = 5 * time.Second` constant. Task 4 wires both into `Watcher`.
- No build tag — pure `net`/`net/http`, unlike `stats_windows.go` which needs `//go:build windows` for `golang.org/x/sys/windows`.

- [ ] **Step 1: Write the failing tests**

Create `perception-svc/sysmonitor/servicehealth_test.go`:

```go
package sysmonitor

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPTCPHealthChecker_HTTPHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	healthy, detail := (httpTCPHealthChecker{}).Check(srv.URL, time.Second)
	if !healthy {
		t.Errorf("healthy = false, want true (detail=%q)", detail)
	}
}

func TestHTTPTCPHealthChecker_HTTPUnhealthyStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	healthy, detail := (httpTCPHealthChecker{}).Check(srv.URL, time.Second)
	if healthy {
		t.Error("healthy = true, want false for a 503 response")
	}
	if detail != "status 503" {
		t.Errorf("detail = %q, want %q", detail, "status 503")
	}
}

func TestHTTPTCPHealthChecker_HTTPUnreachable(t *testing.T) {
	// Nothing listens on this port: 127.0.0.1:1 is a reserved low port
	// that refuses connections immediately rather than timing out.
	healthy, detail := (httpTCPHealthChecker{}).Check("http://127.0.0.1:1/health", time.Second)
	if healthy {
		t.Error("healthy = true, want false for an unreachable HTTP target")
	}
	if detail == "" {
		t.Error("detail = empty, want a non-empty error description")
	}
}

func TestHTTPTCPHealthChecker_TCPHealthy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	healthy, detail := (httpTCPHealthChecker{}).Check(ln.Addr().String(), time.Second)
	if !healthy {
		t.Errorf("healthy = false, want true (detail=%q)", detail)
	}
}

func TestHTTPTCPHealthChecker_TCPUnhealthy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close() // closed immediately: nothing listens on addr anymore

	healthy, detail := (httpTCPHealthChecker{}).Check(addr, time.Second)
	if healthy {
		t.Error("healthy = true, want false for a closed port")
	}
	if detail == "" {
		t.Error("detail = empty, want a non-empty error description")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C perception-svc test ./sysmonitor/... -run TestHTTPTCPHealthChecker -v`
Expected: FAIL — compile error, `httpTCPHealthChecker` undefined.

- [ ] **Step 3: Write the implementation**

Create `perception-svc/sysmonitor/servicehealth.go`:

```go
package sysmonitor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// serviceHealthTimeout bounds every service_health dial/GET — generous
// enough to avoid false positives on a slow-but-alive local service, short
// enough not to stall the poll loop. Not configurable per check (see design
// spec's Out of Scope section).
const serviceHealthTimeout = 5 * time.Second

// healthChecker is the seam between runCheck and the actual network probe
// for service_health checks. Deliberately separate from statsProvider,
// which mirrors local OS syscalls (golang.org/x/sys/windows) — this is
// network I/O with its own failure modes (timeouts, DNS, refused
// connections, HTTP status codes). Tests inject a fake; httpTCPHealthChecker
// is the real implementation.
type healthChecker interface {
	Check(target string, timeout time.Duration) (healthy bool, detail string)
}

// httpTCPHealthChecker implements healthChecker. target is polymorphic,
// detected by prefix: "http://"/"https://" issues a GET and treats any 2xx
// status as healthy; anything else is treated as "host:port" for a raw TCP
// dial. See docs/superpowers/specs/2026-07-19-system-monitor-service-health-design.md.
type httpTCPHealthChecker struct{}

func (httpTCPHealthChecker) Check(target string, timeout time.Duration) (bool, string) {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return checkHTTP(target, timeout)
	}
	return checkTCP(target, timeout)
}

func checkHTTP(target string, timeout time.Duration) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false, err.Error()
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, ""
	}
	return false, fmt.Sprintf("status %d", resp.StatusCode)
}

func checkTCP(target string, timeout time.Duration) (bool, string) {
	conn, err := net.DialTimeout("tcp", target, timeout)
	if err != nil {
		return false, err.Error()
	}
	conn.Close()
	return true, ""
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C perception-svc test ./sysmonitor/... -run TestHTTPTCPHealthChecker -v`
Expected: PASS (all 5 new tests).

- [ ] **Step 5: Commit**

```bash
git add perception-svc/sysmonitor/servicehealth.go perception-svc/sysmonitor/servicehealth_test.go
git commit -m "perception-svc/sysmonitor: add healthChecker seam and real TCP/HTTP implementation"
```

---

### Task 4: `perception-svc/sysmonitor` — wire `service_health` into `Watcher`

**Files:**
- Modify: `perception-svc/sysmonitor/sysmonitor.go` (whole-file rewrite of the sections below)
- Modify: `perception-svc/sysmonitor/stats_windows.go:19-21` (`New`)
- Modify: `perception-svc/sysmonitor/sysmonitor_test.go` (existing `newWatcher` call sites + new tests)

**Interfaces:**
- Consumes: `healthChecker`, `httpTCPHealthChecker`, `serviceHealthTimeout` (Task 3).
- Produces: `sysmonitor.CheckConfig` gains `Name`/`Target` fields; `newWatcher` and `New` gain a `health healthChecker` parameter — Task 5 (`perception-svc/main.go`) must pass `c.Name`/`c.Target` through and Task 4's own `New` already supplies the real `httpTCPHealthChecker{}`.

- [ ] **Step 1: Write the failing tests**

Add to `perception-svc/sysmonitor/sysmonitor_test.go`. First, add a `fakeHealth` type near `fakeStats` (after line 43, before `fakePublisher`):

```go
type fakeHealth struct {
	mu      sync.Mutex
	healthy map[string]bool
	detail  map[string]string
}

func (f *fakeHealth) Check(target string, timeout time.Duration) (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.healthy[target], f.detail[target]
}

func (f *fakeHealth) set(target string, healthy bool, detail string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.healthy == nil {
		f.healthy = map[string]bool{}
	}
	if f.detail == nil {
		f.detail = map[string]string{}
	}
	f.healthy[target] = healthy
	f.detail[target] = detail
}
```

Add a `serviceCheck` helper next to `diskCheck` (after line 69):

```go
func serviceCheck(name, target string) CheckConfig {
	return CheckConfig{Type: "service_health", Name: name, Target: target}
}
```

Update every existing `newWatcher(...)` call site to pass `nil` for the new `health` parameter (these tests never configure a `service_health` check, so `health` is never dereferenced) — eight call sites, each changes from `newWatcher(stats, checks, pub, interval)` to `newWatcher(stats, nil, checks, pub, interval)`:

- Line 74 (`TestPoll_NoThresholdCrossed_NoStimulus`): `w := newWatcher(stats, nil, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)`
- Line 87 (`TestPoll_CrossesIntoWarning_PublishesOnce`): `w := newWatcher(stats, nil, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)`
- Line 111 (`TestPoll_EscalatesToCriticalThenRecovers`): `w := newWatcher(stats, nil, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)`
- Line 147 (`TestPoll_FirstPollAlreadyCritical_PublishesImmediately`): `w := newWatcher(stats, nil, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)`
- Line 163 (`TestPoll_CheckErrorSkipsThatCheckOnly`): `w := newWatcher(stats, nil, checks, pub, time.Hour)`
- Line 175 (`TestPoll_PublishFailure_StateNotAdvanced_RetriesNextPoll`): `w := newWatcher(stats, nil, []CheckConfig{diskCheck(`C:\`)}, pub, time.Hour)`
- Line 201 (`TestPoll_MultipleDiskPaths_TrackedIndependently`): `w := newWatcher(stats, nil, checks, pub, time.Hour)`
- Line 214 (`TestPoll_CPUNoBaselineFirstCall_SkippedSilently`): `w := newWatcher(stats, nil, checks, pub, time.Hour)`

Add new tests at the end of the file (after `severityFromStimulus`, keep `severityFromStimulus` as the last function):

```go
func TestPoll_ServiceHealth_NoChange_NoStimulus(t *testing.T) {
	health := &fakeHealth{healthy: map[string]bool{"svc:1234": true}}
	pub := &fakePublisher{}
	w := newWatcher(&fakeStats{}, health, []CheckConfig{serviceCheck("svc", "svc:1234")}, pub, time.Hour)

	w.poll(context.Background())
	w.poll(context.Background())

	if got := pub.publishedCount(); got != 0 {
		t.Errorf("published = %d, want 0 (steady healthy state)", got)
	}
}

func TestPoll_ServiceHealth_GoesDownThenRecovers(t *testing.T) {
	health := &fakeHealth{healthy: map[string]bool{"svc:1234": true}}
	pub := &fakePublisher{}
	w := newWatcher(&fakeStats{}, health, []CheckConfig{serviceCheck("svc", "svc:1234")}, pub, time.Hour)

	w.poll(context.Background()) // healthy baseline, no stimulus

	health.set("svc:1234", false, "dial tcp svc:1234: connect: connection refused")
	w.poll(context.Background()) // ok -> critical

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1", got)
	}
	if pub.published[0].Hints.Priority != "critical" {
		t.Errorf("Hints.Priority = %q, want critical", pub.published[0].Hints.Priority)
	}
	wantText := "Service down: svc unreachable (dial tcp svc:1234: connect: connection refused)"
	if got := pub.published[0].Content.RawText; got != wantText {
		t.Errorf("RawText = %q, want %q", got, wantText)
	}

	health.set("svc:1234", true, "")
	w.poll(context.Background()) // critical -> ok

	if got := pub.publishedCount(); got != 2 {
		t.Fatalf("published = %d, want 2 (down + recovery)", got)
	}
	if pub.published[1].Content.RawText != "Service recovered: svc is back up" {
		t.Errorf("recovery RawText = %q, want %q", pub.published[1].Content.RawText, "Service recovered: svc is back up")
	}
	if pub.published[1].Hints.Priority != "normal" {
		t.Errorf("recovery Hints.Priority = %q, want normal", pub.published[1].Hints.Priority)
	}
}

func TestPoll_ServiceHealth_FirstPollAlreadyDown_PublishesImmediately(t *testing.T) {
	health := &fakeHealth{
		healthy: map[string]bool{"svc:1234": false},
		detail:  map[string]string{"svc:1234": "dial tcp: i/o timeout"},
	}
	pub := &fakePublisher{}
	w := newWatcher(&fakeStats{}, health, []CheckConfig{serviceCheck("svc", "svc:1234")}, pub, time.Hour)

	w.poll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (already-down state must fire on first poll, not be treated as a baseline)", got)
	}
}

func TestPoll_ServiceHealth_PublishFailure_StateNotAdvanced_RetriesNextPoll(t *testing.T) {
	health := &fakeHealth{healthy: map[string]bool{"svc:1234": true}}
	pub := &fakePublisher{}
	w := newWatcher(&fakeStats{}, health, []CheckConfig{serviceCheck("svc", "svc:1234")}, pub, time.Hour)

	w.poll(context.Background()) // healthy baseline

	health.set("svc:1234", false, "connection refused")
	pub.publishErr = errors.New("nats down")
	w.poll(context.Background()) // ok -> critical, publish fails

	if got := pub.publishedCount(); got != 0 {
		t.Fatalf("published = %d, want 0 (publish failed)", got)
	}

	pub.publishErr = nil
	w.poll(context.Background()) // retry: still ok -> critical transition

	if got := pub.publishedCount(); got != 1 {
		t.Errorf("published = %d, want 1 (transition retried after publish recovered)", got)
	}
}

func TestPoll_ServiceHealthAndDiskCheck_TrackedIndependently(t *testing.T) {
	stats := &fakeStats{disk: map[string]float64{`C:\`: 50}}
	health := &fakeHealth{healthy: map[string]bool{"svc:1234": false}}
	pub := &fakePublisher{}
	checks := []CheckConfig{diskCheck(`C:\`), serviceCheck("svc", "svc:1234")}
	w := newWatcher(stats, health, checks, pub, time.Hour)

	w.poll(context.Background())

	if got := pub.publishedCount(); got != 1 {
		t.Fatalf("published = %d, want 1 (only the service check starts down)", got)
	}

	var meta struct {
		CheckType string `json:"check_type"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal(pub.published[0].ChannelMeta.ChannelSpecific, &meta); err != nil {
		t.Fatalf("decode channel_specific: %v", err)
	}
	if meta.CheckType != "service_health" || meta.Name != "svc" {
		t.Errorf("channel_specific = %+v, want check_type=service_health name=svc", meta)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C perception-svc test ./sysmonitor/... -v`
Expected: FAIL — compile errors (`CheckConfig` has no `Name`/`Target` fields, `newWatcher` takes 4 args not 5, `fakeHealth` unused-otherwise-fine but `serviceCheck`/new tests reference `Type: "service_health"` which isn't handled yet).

- [ ] **Step 3: Rewrite `sysmonitor.go`**

Replace the whole file `perception-svc/sysmonitor/sysmonitor.go` with:

```go
// Package sysmonitor implements perception-svc's System Monitor pull
// channel: polls disk space, memory, CPU usage, and external service
// health on a fixed interval and publishes a Stimulus only when a check's
// severity changes — see
// docs/superpowers/specs/2026-07-18-system-monitor-channel-design.md and
// docs/superpowers/specs/2026-07-19-system-monitor-service-health-design.md.
package sysmonitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"soulman/common"
)

// Publisher is satisfied by *natspublish.Publisher. Declared here (not
// imported from natspublish) to avoid an import cycle — mirrors
// watcher.Publisher and gmailwatcher.Publisher's same rationale.
type Publisher interface {
	Publish(ctx context.Context, s *common.Stimulus) error
}

// statsProvider is the seam between the poll loop and the actual OS calls.
// Tests inject a fake; stats_windows.go's winStats is the real
// golang.org/x/sys/windows-backed implementation.
type statsProvider interface {
	DiskUsagePercent(path string) (float64, error)
	MemoryUsagePercent() (float64, error)
	CPUUsagePercent() (float64, error)
}

// errNoCPUBaseline signals that this is the first CPU reading since process
// startup (or since a prior reading), so there is no previous cumulative
// snapshot to diff against — treated as a silent skip, not a logged error.
var errNoCPUBaseline = errors.New("sysmonitor: no cpu baseline yet")

// CheckConfig describes one system-monitor check, mirroring
// sharedconfig.CheckConfig (perception-svc/main.go converts one into the
// other so this package doesn't import sharedconfig directly). Name and
// Target are service_health-only; WarningThresholdPercent/
// CriticalThresholdPercent are unused by service_health (its severity is
// binary, derived from healthChecker.Check instead).
type CheckConfig struct {
	Type                     string
	Path                     string
	Name                     string
	Target                   string
	WarningThresholdPercent  float64
	CriticalThresholdPercent float64
}

type severity string

const (
	severityOK       severity = "ok"
	severityWarning  severity = "warning"
	severityCritical severity = "critical"
)

// Watcher polls the configured checks and publishes a Stimulus on each
// severity transition. State is in-memory only (see design spec) — a
// restart resets every check to "ok", so a still-bad condition re-fires
// once more, an accepted tradeoff simpler than a persisted checkpoint.
type Watcher struct {
	checks    []CheckConfig
	stats     statsProvider
	health    healthChecker
	publisher Publisher
	interval  time.Duration

	state map[string]severity

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
	}
}

// Start launches the poll loop in a background goroutine and returns
// immediately, running one immediate poll first (so a bad state is caught
// right away, not after a full interval) — same pattern as
// gmailwatcher.Start.
func (w *Watcher) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	go w.pollLoop(ctx)
}

func (w *Watcher) pollLoop(ctx context.Context) {
	w.poll(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

// poll runs every configured check once. Each check is independent — one
// check's failure is logged and skipped, the rest still run, mirroring
// watcher's per-file isolation.
func (w *Watcher) poll(ctx context.Context) {
	for _, c := range w.checks {
		w.runCheck(ctx, c)
	}
}

func checkKey(c CheckConfig) string {
	switch c.Type {
	case "disk_space":
		return c.Type + ":" + c.Path
	case "service_health":
		return c.Type + ":" + c.Name
	default:
		return c.Type
	}
}

func (w *Watcher) measure(c CheckConfig) (float64, error) {
	switch c.Type {
	case "disk_space":
		return w.stats.DiskUsagePercent(c.Path)
	case "memory":
		return w.stats.MemoryUsagePercent()
	case "cpu":
		return w.stats.CPUUsagePercent()
	default:
		return 0, fmt.Errorf("sysmonitor: unknown check type %q", c.Type)
	}
}

func deriveSeverity(value, warning, critical float64) severity {
	if critical > 0 && value >= critical {
		return severityCritical
	}
	if value >= warning {
		return severityWarning
	}
	return severityOK
}

// runCheck measures one check and hands the result to publishTransition,
// which owns the edge-triggered state machine shared by every check type.
// service_health bypasses measure/deriveSeverity entirely: its severity is
// binary, derived directly from healthChecker.Check.
func (w *Watcher) runCheck(ctx context.Context, c CheckConfig) {
	if c.Type == "service_health" {
		healthy, detail := w.health.Check(c.Target, serviceHealthTimeout)
		sev := severityOK
		if !healthy {
			sev = severityCritical
		}
		w.publishTransition(ctx, checkKey(c), sev, func() *common.Stimulus {
			return buildServiceHealthStimulus(c, sev, detail)
		})
		return
	}

	value, err := w.measure(c)
	if err != nil {
		if errors.Is(err, errNoCPUBaseline) {
			return
		}
		log.Printf("sysmonitor: check %s failed, skipping this poll: %v", checkKey(c), err)
		return
	}

	sev := deriveSeverity(value, c.WarningThresholdPercent, c.CriticalThresholdPercent)
	w.publishTransition(ctx, checkKey(c), sev, func() *common.Stimulus {
		return buildStimulus(c, value, sev)
	})
}

// publishTransition holds the edge-triggered state machine every check
// type shares: no stimulus on a steady state, no stimulus on a healthy/ok
// first sighting (baseline only), publish on any other transition, and
// leave state unadvanced (so the transition retries next poll) if the
// publish itself fails.
func (w *Watcher) publishTransition(ctx context.Context, key string, sev severity, build func() *common.Stimulus) {
	prev, seen := w.state[key]
	if seen && prev == sev {
		return
	}
	if !seen && sev == severityOK {
		w.state[key] = sev
		return
	}
	if err := w.publisher.Publish(ctx, build()); err != nil {
		log.Printf("sysmonitor: publish failed for %s (state unchanged, will retry next poll): %v", key, err)
		return
	}
	w.state[key] = sev
}

func checkLabel(c CheckConfig) string {
	switch c.Type {
	case "disk_space":
		return fmt.Sprintf("Disk space %s", c.Path)
	case "memory":
		return "Memory usage"
	case "cpu":
		return "CPU usage"
	default:
		return c.Type
	}
}

func formatMessage(c CheckConfig, value float64, sev severity, threshold float64) string {
	label := checkLabel(c)
	switch sev {
	case severityOK:
		return fmt.Sprintf("%s recovered to normal: %.0f%% used", label, value)
	case severityWarning:
		return fmt.Sprintf("%s warning: %.0f%% used (threshold %.0f%%)", label, value, threshold)
	default:
		return fmt.Sprintf("%s critical: %.0f%% used (threshold %.0f%%)", label, value, threshold)
	}
}

// formatServiceHealthMessage mirrors formatMessage but for the binary
// service_health check type, which has no percentage/threshold to report.
func formatServiceHealthMessage(c CheckConfig, sev severity, detail string) string {
	if sev == severityOK {
		return fmt.Sprintf("Service recovered: %s is back up", c.Name)
	}
	return fmt.Sprintf("Service down: %s unreachable (%s)", c.Name, detail)
}

func priorityFor(sev severity) string {
	switch sev {
	case severityCritical:
		return "critical"
	case severityWarning:
		return "high"
	default:
		return "normal"
	}
}

// computeMessageID's second argument is the check's Path for disk_space,
// or its Name for service_health — either way, whatever distinguishes this
// check from others of the same Type.
func computeMessageID(checkType, pathOrName string, sev severity, at time.Time) string {
	sum := sha256.Sum256([]byte(checkType + pathOrName + string(sev) + at.Format(time.RFC3339)))
	return hex.EncodeToString(sum[:])
}

// newSystemMonitorStimulus builds the envelope every system-monitor
// Stimulus shares (Source, Content shell, ChannelMeta.MessageID, Hints,
// Override) — buildStimulus and buildServiceHealthStimulus each fill in
// ChannelMeta.ChannelSpecific afterward, since that shape differs by type.
func newSystemMonitorStimulus(now time.Time, checkType, messageID, rawText string, sev severity) *common.Stimulus {
	id, err := uuid.NewV7()
	if err != nil {
		// Extremely unlikely (crypto/rand failure); fall back to a random v4
		// rather than crash the watcher over one reading.
		id = uuid.New()
	}

	return &common.Stimulus{
		StimulusID:    id.String(),
		SchemaVersion: 1,
		ReceivedAt:    now,
		OccurredAt:    &now,
		Channel:       "system-monitor",
		Source: common.Source{
			Identity:      "system-monitor",
			Authenticated: true,
			AuthMethod:    "system",
		},
		Content: common.Content{
			RawText:     rawText,
			RawPayload:  json.RawMessage(`{}`),
			ContentType: "text",
			Attachments: []common.Attachment{},
		},
		ChannelMeta: common.ChannelMeta{
			MessageID: messageID,
		},
		Hints: common.Hints{
			Priority: priorityFor(sev),
			Tags:     []string{"system", "system-monitor", checkType},
		},
		Override: common.Override{
			Params: json.RawMessage(`{}`),
		},
	}
}

func buildStimulus(c CheckConfig, value float64, sev severity) *common.Stimulus {
	now := time.Now().UTC()

	threshold := c.WarningThresholdPercent
	if sev == severityCritical {
		threshold = c.CriticalThresholdPercent
	}

	specific, _ := json.Marshal(struct {
		CheckType        string  `json:"check_type"`
		Path             string  `json:"path,omitempty"`
		Severity         string  `json:"severity"`
		ValuePercent     float64 `json:"value_percent"`
		ThresholdPercent float64 `json:"threshold_percent"`
	}{
		CheckType:        c.Type,
		Path:             c.Path,
		Severity:         string(sev),
		ValuePercent:     value,
		ThresholdPercent: threshold,
	})

	s := newSystemMonitorStimulus(now, c.Type, computeMessageID(c.Type, c.Path, sev, now), formatMessage(c, value, sev, threshold), sev)
	s.ChannelMeta.ChannelSpecific = specific
	return s
}

// buildServiceHealthStimulus mirrors buildStimulus but for the binary
// service_health check type — see docs/superpowers/specs/2026-07-19-system-monitor-service-health-design.md.
func buildServiceHealthStimulus(c CheckConfig, sev severity, detail string) *common.Stimulus {
	now := time.Now().UTC()

	errField := ""
	if sev == severityCritical {
		errField = detail
	}

	specific, _ := json.Marshal(struct {
		CheckType string `json:"check_type"`
		Name      string `json:"name"`
		Severity  string `json:"severity"`
		Error     string `json:"error,omitempty"`
	}{
		CheckType: c.Type,
		Name:      c.Name,
		Severity:  string(sev),
		Error:     errField,
	})

	s := newSystemMonitorStimulus(now, c.Type, computeMessageID(c.Type, c.Name, sev, now), formatServiceHealthMessage(c, sev, detail), sev)
	s.ChannelMeta.ChannelSpecific = specific
	return s
}

func (w *Watcher) Close() error {
	if w.cancel != nil {
		w.cancel()
	}
	return nil
}
```

- [ ] **Step 4: Update `New` in `stats_windows.go`**

In `perception-svc/sysmonitor/stats_windows.go`, replace `New` (lines 19-21):

```go
// New builds a Watcher backed by real Windows system calls
// (golang.org/x/sys/windows — already an indirect dependency of this
// module via oauth2/nats.go, promoted to direct for this package) for
// disk, memory, and CPU statistics, and a real TCP/HTTP client for
// service_health checks.
func New(checks []CheckConfig, publisher Publisher, interval time.Duration) *Watcher {
	return newWatcher(&winStats{}, httpTCPHealthChecker{}, checks, publisher, interval)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go -C perception-svc build ./... && go -C perception-svc test ./sysmonitor/... -v`
Expected: PASS — every pre-existing test (disk/memory/CPU) and every new `service_health` test, plus a clean build (confirms `stats_windows.go`'s updated `New` call and `perception-svc/main.go`'s still-unmodified call to `sysmonitor.New` still type-check — Task 5 fixes `main.go` itself next, but this step surfaces the compile break early if it exists).

- [ ] **Step 6: Commit**

```bash
git add perception-svc/sysmonitor/sysmonitor.go perception-svc/sysmonitor/stats_windows.go perception-svc/sysmonitor/sysmonitor_test.go
git commit -m "perception-svc/sysmonitor: wire service_health into Watcher's poll loop"
```

---

### Task 5: `perception-svc/main.go` — pass `Name`/`Target` through

**Files:**
- Modify: `perception-svc/main.go:71-79`

**Interfaces:**
- Consumes: `sharedconfig.CheckConfig.Name`/`.Target` (Task 1), `sysmonitor.CheckConfig.Name`/`.Target` (Task 4).

This task has no new unit test of its own — `perception-svc/main.go` is a `package main` wiring file with no existing test coverage (consistent with the rest of this file), and its correctness here is a straight field-for-field passthrough already covered end-to-end by Task 6's `thinking-svc` test and by manually starting the service (Step 3 below).

- [ ] **Step 1: Update the conversion loop**

In `perception-svc/main.go`, replace lines 71-79:

```go
	smChecks := make([]sysmonitor.CheckConfig, len(cfg.SystemMonitorChecks))
	for i, c := range cfg.SystemMonitorChecks {
		smChecks[i] = sysmonitor.CheckConfig{
			Type:                     c.Type,
			Path:                     c.Path,
			Name:                     c.Name,
			Target:                   c.Target,
			WarningThresholdPercent:  c.WarningThresholdPercent,
			CriticalThresholdPercent: c.CriticalThresholdPercent,
		}
	}
	sm := sysmonitor.New(smChecks, pub, time.Duration(cfg.SystemMonitorPollIntervalSeconds)*time.Second)
	defer sm.Close()
	sm.Start(ctx)
	log.Printf("sysmonitor: started (checks=%d, poll_interval=%ds)", len(smChecks), cfg.SystemMonitorPollIntervalSeconds)
```

(Only the two new struct fields, `Name` and `Target`, are added to the literal — everything else on these lines is unchanged.)

- [ ] **Step 2: Verify the build**

Run: `go -C perception-svc build ./...`
Expected: succeeds with no errors.

- [ ] **Step 3: Commit**

```bash
git add perception-svc/main.go
git commit -m "perception-svc: pass service_health Name/Target through to sysmonitor.Watcher"
```

---

### Task 6: `thinking-svc/rules/system_monitor.go` — resolve `source_path` via `name`

**Files:**
- Modify: `thinking-svc/rules/system_monitor.go:57-69`
- Test: `thinking-svc/rules/system_monitor_test.go`

**Interfaces:**
- Consumes: `channel_metadata.channel_specific.name`, produced by `buildServiceHealthStimulus` (Task 4).
- Produces: `systemMonitorSourcePath` now resolves `system-monitor/service_health/<name>` for a `service_health` stimulus (previously fell back to the bare `system-monitor/service_health`, since it only read `path`).

- [ ] **Step 1: Write the failing test**

Add to `thinking-svc/rules/system_monitor_test.go`. First add a helper mirroring `newSystemMonitorStimulus` but with a `name` field, right after that function (after line 35):

```go
func newServiceHealthStimulus(rawText, name string, occurredAt time.Time) *common.Stimulus {
	specific, _ := json.Marshal(struct {
		CheckType string `json:"check_type"`
		Name      string `json:"name"`
	}{CheckType: "service_health", Name: name})

	return &common.Stimulus{
		StimulusID: "stim-sysmon-002",
		Channel:    "system-monitor",
		ReceivedAt: time.Now().UTC(),
		OccurredAt: &occurredAt,
		Content: common.Content{
			RawText:     rawText,
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
		},
		ChannelMeta: common.ChannelMeta{
			ChannelSpecific: specific,
		},
		Hints:    common.Hints{Priority: "critical", Tags: []string{"system", "system-monitor", "service_health"}},
		Override: common.Override{Params: json.RawMessage(`{}`)},
	}
}

func TestSystemMonitorRule_Handle_BuildsActionRequest_ServiceHealthName(t *testing.T) {
	occurred := time.Date(2026, 7, 19, 10, 5, 0, 0, time.UTC)
	rawText := "Service down: agent-suite-backend unreachable (connection refused)"
	s := newServiceHealthStimulus(rawText, "agent-suite-backend", occurred)

	req, err := rules.SystemMonitorRule.Handle(context.Background(), s, &fakeSummarizer{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var params struct {
		SourcePath string `json:"source_path"`
	}
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	if params.SourcePath != "system-monitor/service_health/agent-suite-backend" {
		t.Errorf("SourcePath = %q, want %q", params.SourcePath, "system-monitor/service_health/agent-suite-backend")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go -C thinking-svc test ./rules/... -run TestSystemMonitorRule_Handle_BuildsActionRequest_ServiceHealthName -v`
Expected: FAIL — `SourcePath = "system-monitor/service_health"`, want `"system-monitor/service_health/agent-suite-backend"` (the `name` field is present in the stimulus but not yet read).

- [ ] **Step 3: Update `systemMonitorSourcePath`**

In `thinking-svc/rules/system_monitor.go`, replace `systemMonitorSourcePath` (lines 57-69):

```go
// systemMonitorSourcePath builds "system-monitor/<check_type>",
// "system-monitor/<check_type>/<path>", or "system-monitor/<check_type>/<name>"
// from channel_metadata.channel_specific — path is disk_space's identifier,
// name is service_health's. Parallels error_report.go's watchedPath()
// extraction helper.
func systemMonitorSourcePath(s *common.Stimulus) string {
	var meta struct {
		CheckType string `json:"check_type"`
		Path      string `json:"path"`
		Name      string `json:"name"`
	}
	if len(s.ChannelMeta.ChannelSpecific) > 0 {
		_ = json.Unmarshal(s.ChannelMeta.ChannelSpecific, &meta)
	}
	id := meta.Path
	if id == "" {
		id = meta.Name
	}
	if id == "" {
		return "system-monitor/" + meta.CheckType
	}
	return "system-monitor/" + meta.CheckType + "/" + id
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C thinking-svc test ./rules/... -v`
Expected: PASS — the new test and every pre-existing `system_monitor_test.go` test (the `path`-based tests still resolve correctly since `meta.Path` is checked first).

- [ ] **Step 5: Commit**

```bash
git add thinking-svc/rules/system_monitor.go thinking-svc/rules/system_monitor_test.go
git commit -m "thinking-svc: resolve system-monitor source_path via name for service_health"
```

---

### Task 7: `config/dev.json` and `config/prod.json` — add the four services

**Files:**
- Modify: `config/dev.json:21-28`
- Modify: `config/prod.json:21-28`

**Interfaces:** None (config data only — Tasks 2 and 4 already validate/consume this shape).

- [ ] **Step 1: Update `config/dev.json`**

Replace the `system_monitor` block (lines 21-28):

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
      { "type": "service_health", "name": "agent-suite-backend", "target": "http://localhost:8091/health" }
    ]
  },
```

- [ ] **Step 2: Update `config/prod.json`**

Replace the `system_monitor` block (lines 21-28) with the same four `service_health` entries appended, identically (dev and prod share this config verbatim today — same as the existing disk/memory/CPU entries):

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
      { "type": "service_health", "name": "agent-suite-backend", "target": "http://localhost:8091/health" }
    ]
  },
```

- [ ] **Step 3: Verify both files are valid JSON**

Run: `powershell -Command "Get-Content config/dev.json | ConvertFrom-Json | Out-Null; Get-Content config/prod.json | ConvertFrom-Json | Out-Null; Write-Output OK"`
Expected: `OK` printed, no parse errors.

- [ ] **Step 4: Commit**

```bash
git add config/dev.json config/prod.json
git commit -m "config: add service_health checks for digital-me and agent-suite frontend/backend"
```

---

### Task 8: Update `perception-svc/NOTES.md` and root `CLAUDE.md`

**Files:**
- Modify: `perception-svc/NOTES.md` (System Monitor channel section, lines 22-28)
- Modify: `CLAUDE.md` (perception-svc row/spec list in the Services section)

**Interfaces:** None (documentation only).

- [ ] **Step 1: Add a paragraph to `perception-svc/NOTES.md`**

In `perception-svc/NOTES.md`, after the existing "System Monitor channel" section's last paragraph (after line 28, the "Dev and prod both poll the same physical machine's..." paragraph), add:

```markdown

`service_health` (added 2026-07-19, see `docs/superpowers/specs/2026-07-19-system-monitor-service-health-design.md`) is a fourth check type, binary (`ok`/`critical`, no `warning` tier) rather than threshold-derived — it probes an external target instead of a local syscall via a separate `healthChecker` seam (`servicehealth.go`), not `statsProvider`. `target` is polymorphic: `http://`/`https://` → GET, any 2xx is healthy; bare `host:port` → TCP dial. Both share the same 300s poll interval and edge-triggered state machine as disk/memory/CPU; the dial/GET timeout is a fixed 5s constant, not configurable per check.
```

- [ ] **Step 2: Update `CLAUDE.md`'s perception-svc row**

In `CLAUDE.md`, find the `perception-svc` bullet in the Services section (its Specs line currently ends with `2026-07-18-system-monitor-channel-design.md`). Append the new spec to that Specs list:

Find this substring in the perception-svc bullet:

```
- Specs: `2026-07-17-perception-svc-design.md`, `2026-07-18-gmail-channel-design.md`, `2026-07-18-soulman-cli-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-18-system-monitor-channel-design.md`
```

Replace it with:

```
- Specs: `2026-07-17-perception-svc-design.md`, `2026-07-18-gmail-channel-design.md`, `2026-07-18-soulman-cli-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-18-system-monitor-channel-design.md`, `2026-07-19-system-monitor-service-health-design.md`
```

Also update the perception-svc row's summary sentence — find:

```
Three input channels: **folder-watcher** (`fsnotify` on paths from the shared config file's `watch_paths`), **Gmail** (`gmailwatcher` package — polls the inbox via OAuth2 offline refresh token, dedups via a per-environment Gmail label), and **System Monitor** (`sysmonitor` package — polls disk/memory/CPU via `golang.org/x/sys/windows`, publishes only on a severity transition).
```

Replace with:

```
Three input channels: **folder-watcher** (`fsnotify` on paths from the shared config file's `watch_paths`), **Gmail** (`gmailwatcher` package — polls the inbox via OAuth2 offline refresh token, dedups via a per-environment Gmail label), and **System Monitor** (`sysmonitor` package — polls disk/memory/CPU via `golang.org/x/sys/windows` plus external `service_health` targets via TCP dial/HTTP GET, publishes only on a severity transition).
```

- [ ] **Step 3: Commit**

```bash
git add perception-svc/NOTES.md CLAUDE.md
git commit -m "docs: note service_health check type in perception-svc NOTES.md and CLAUDE.md"
```

---

## Final Verification

After all 8 tasks:

- [ ] Run every module's full test suite: `go -C common test ./... && go -C perception-svc test ./... && go -C thinking-svc test ./...` — expect all PASS.
- [ ] Run `go -C perception-svc build ./... && go -C thinking-svc build ./... && go -C action-svc build ./... && go -C memory-svc build ./... && go -C web-svc build ./...` — expect all services still build (action-svc/memory-svc/web-svc are untouched by this plan, but confirm no accidental breakage from the shared `common` module change in Task 1).
- [ ] Manually start `perception-svc` against dev config (`config/dev.json` after Task 7) and confirm the startup log line `sysmonitor: started (checks=7, poll_interval=300s)` (7 = 3 existing + 4 new `service_health` checks) with no fatal config errors.
