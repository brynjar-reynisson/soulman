// Package sysmonitor implements perception-svc's System Monitor pull
// channel: polls disk space, memory, and CPU usage on a fixed interval and
// publishes a Stimulus only when a check's severity (ok/warning/critical)
// changes — see docs/superpowers/specs/2026-07-18-system-monitor-channel-design.md.
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
// other so this package doesn't import sharedconfig directly).
type CheckConfig struct {
	Type                     string
	Path                     string
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
	publisher Publisher
	interval  time.Duration

	state map[string]severity

	cancel context.CancelFunc
}

func newWatcher(stats statsProvider, checks []CheckConfig, publisher Publisher, interval time.Duration) *Watcher {
	return &Watcher{
		checks:    checks,
		stats:     stats,
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
	if c.Type == "disk_space" {
		return c.Type + ":" + c.Path
	}
	return c.Type
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

func (w *Watcher) runCheck(ctx context.Context, c CheckConfig) {
	value, err := w.measure(c)
	if err != nil {
		if errors.Is(err, errNoCPUBaseline) {
			return
		}
		log.Printf("sysmonitor: check %s failed, skipping this poll: %v", checkKey(c), err)
		return
	}

	sev := deriveSeverity(value, c.WarningThresholdPercent, c.CriticalThresholdPercent)
	key := checkKey(c)
	prev, seen := w.state[key]

	if seen && prev == sev {
		return
	}
	if !seen && sev == severityOK {
		// First-ever poll for this check and it's already fine: nothing to
		// report. Still record the baseline so a future flip is detected.
		w.state[key] = sev
		return
	}

	stimulus := buildStimulus(c, value, sev)
	if err := w.publisher.Publish(ctx, stimulus); err != nil {
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

func computeMessageID(checkType, path string, sev severity, at time.Time) string {
	sum := sha256.Sum256([]byte(checkType + path + string(sev) + at.Format(time.RFC3339)))
	return hex.EncodeToString(sum[:])
}

func buildStimulus(c CheckConfig, value float64, sev severity) *common.Stimulus {
	now := time.Now().UTC()
	id, err := uuid.NewV7()
	if err != nil {
		// Extremely unlikely (crypto/rand failure); fall back to a random v4
		// rather than crash the watcher over one reading.
		id = uuid.New()
	}

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
			RawText:     formatMessage(c, value, sev, threshold),
			RawPayload:  json.RawMessage(`{}`),
			ContentType: "text",
			Attachments: []common.Attachment{},
		},
		ChannelMeta: common.ChannelMeta{
			MessageID:       computeMessageID(c.Type, c.Path, sev, now),
			ChannelSpecific: specific,
		},
		Hints: common.Hints{
			Priority: priorityFor(sev),
			Tags:     []string{"system", "system-monitor", c.Type},
		},
		Override: common.Override{
			Params: json.RawMessage(`{}`),
		},
	}
}

func (w *Watcher) Close() error {
	if w.cancel != nil {
		w.cancel()
	}
	return nil
}
