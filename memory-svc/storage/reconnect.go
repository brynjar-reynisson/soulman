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

// tickTimeout bounds each tick's Postgres calls (newDB's connect+ping,
// or Ping alone) so a black-holed host — packets silently dropped
// rather than refused, the realistic outage class this feature exists
// to catch — can't stall this goroutine for the OS-level TCP
// connect/read timeout (potentially minutes). Reconnector.Run is a
// single goroutine, so tick() blocking means no ticks fire for anyone
// until it returns.
const tickTimeout = 10 * time.Second

func (rc *Reconnector) tick(ctx context.Context) {
	tickCtx, cancel := context.WithTimeout(ctx, tickTimeout)
	defer cancel()

	db := rc.holder.Get()

	if db == nil {
		newDB, err := rc.newDB(tickCtx, rc.connStr, rc.schema)
		if err != nil {
			// Deliberately not logged: this fires every interval for the
			// entire duration of an outage, and undifferentiated retry
			// noise from a real Postgres outage is exactly what produced
			// the 2.75GB log file documented in this service's NOTES.md
			// before leveled logging existed. The registry.Record call
			// still updates Detail with the latest error, and the
			// transition into "down" was already logged (either at
			// startup or by the Ping-failure branch below) — this branch
			// is "still down," not a new event.
			rc.registry.Record("postgres", err)
			return
		}
		rc.holder.set(newDB)
		rc.registry.Record("postgres", nil)
		slog.Info("storage: postgres reconnected")
		return
	}

	if err := db.Ping(tickCtx); err != nil {
		rc.holder.set(nil)
		rc.registry.Record("postgres", err)
		slog.Error("storage: postgres ping failed, marked down", "error", err)
		go db.Close() // may block until in-flight queries release their conns
		return
	}
	rc.registry.Record("postgres", nil)
}
