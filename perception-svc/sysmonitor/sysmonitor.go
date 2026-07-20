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
	"sort"
	"sync"
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
