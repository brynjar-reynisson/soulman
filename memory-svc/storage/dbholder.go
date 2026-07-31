package storage

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

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

// Close closes the currently held connection, if any, and nils out the
// held *DB so a subsequent Get() reports disconnected rather than
// returning an already-closed pool. Previously this only left the pool
// closed without clearing h.db — safe only because of main.go's
// non-obvious defer ordering (Reconnector's context is cancelled before
// Close() runs); any future caller that got that ordering wrong could
// have Get() hand out a closed pool. Flagged during Task 2's review.
func (h *DBHolder) Close() {
	h.mu.Lock()
	db := h.db
	h.db = nil
	h.mu.Unlock()
	if db != nil {
		db.Close()
	}
}

// recordDBOutcome records a real Postgres call's outcome into the
// registry, distinguishing a connectivity failure from a query-level
// error the database itself responded to. A *pgconn.PgError (or
// pgx.ErrNoRows) proves the connection is reachable — Postgres received
// the query and replied — so it is recorded as "ok" even though the
// call itself failed; only errors that indicate the connection is
// actually unreachable (network errors, a closed pool, a context
// deadline on a hung connection) are recorded as "down". Without this
// distinction, an isolated query-level error (e.g. a missing table)
// would falsely latch the dependency "down" until an unrelated call
// happened to succeed.
func recordDBOutcome(registry *dephealth.Registry, name string, err error) {
	if err == nil {
		registry.Record(name, nil)
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) || errors.Is(err, pgx.ErrNoRows) {
		registry.Record(name, nil)
		return
	}
	registry.Record(name, err)
}

func (h *DBHolder) InsertRawInput(ctx context.Context, s *common.Stimulus) error {
	db := h.Get()
	if db == nil {
		return ErrNotConnected
	}
	err := db.InsertRawInput(ctx, s)
	recordDBOutcome(h.registry, "postgres", err)
	return err
}

func (h *DBHolder) GetRecent(ctx context.Context, limit int) ([]RawInput, error) {
	db := h.Get()
	if db == nil {
		return nil, ErrNotConnected
	}
	rows, err := db.GetRecent(ctx, limit)
	recordDBOutcome(h.registry, "postgres", err)
	return rows, err
}

func (h *DBHolder) GetRecentEpisodes(ctx context.Context, limit int) ([]Episode, error) {
	db := h.Get()
	if db == nil {
		return nil, ErrNotConnected
	}
	rows, err := db.GetRecentEpisodes(ctx, limit)
	recordDBOutcome(h.registry, "postgres", err)
	return rows, err
}

func (h *DBHolder) WriteEpisode(ctx context.Context, streamSeq uint64, rec *common.OutcomeRecord) error {
	db := h.Get()
	if db == nil {
		return ErrNotConnected
	}
	err := db.WriteEpisode(ctx, streamSeq, rec)
	recordDBOutcome(h.registry, "postgres", err)
	return err
}
