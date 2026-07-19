// Package feign implements action-svc's dry-run mechanism: a reusable Gate
// that lets any component with an outbound side effect (starting with
// notify.Notifier) record what it would have done instead of doing it. See
// docs/superpowers/specs/2026-07-19-action-svc-feign-mode-design.md.
package feign

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Gate decides whether an outbound side effect actually happens, or is
// only recorded. Any component with something to gate wraps itself around
// a shared *Gate (see WrapNotifier) — this is the one reusable mechanism
// new integrations adopt, rather than each inventing its own suppression
// flag.
type Gate struct {
	enabled bool
	logPath string
	mu      sync.Mutex
}

// New builds a Gate. logPath's parent directory is created on first
// Record call, not eagerly — a Gate that's never enabled never touches
// the filesystem.
func New(enabled bool, logPath string) *Gate {
	return &Gate{enabled: enabled, logPath: logPath}
}

// Enabled reports whether feign mode is on. Nil-receiver-safe (returns
// false) so callers that don't care about feign mode can pass a nil *Gate
// instead of constructing a disabled one. Components that need to phrase
// an outcome record differently depending on mode call this directly,
// since a feigned action and a real one both "succeed" from the caller's
// point of view and can't be told apart by return value alone.
func (g *Gate) Enabled() bool {
	return g != nil && g.enabled
}

type record struct {
	Timestamp time.Time `json:"timestamp"`
	Kind      string    `json:"kind"`
	Detail    string    `json:"detail"`
}

// Record appends one feigned-action entry to logPath as a single JSON
// line. Safe for concurrent use.
func (g *Gate) Record(kind, detail string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(g.logPath), 0o755); err != nil {
		return fmt.Errorf("feign: mkdir for %s: %w", g.logPath, err)
	}

	f, err := os.OpenFile(g.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("feign: open %s: %w", g.logPath, err)
	}
	defer f.Close()

	b, err := json.Marshal(record{Timestamp: time.Now(), Kind: kind, Detail: detail})
	if err != nil {
		return fmt.Errorf("feign: marshal record: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("feign: write to %s: %w", g.logPath, err)
	}
	return nil
}
