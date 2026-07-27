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
	} else if seen && since.Equal(prev.Since) {
		// On systems with low timer resolution, ensure transition timestamps differ
		since = prev.Since.Add(time.Nanosecond)
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
