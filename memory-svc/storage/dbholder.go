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
