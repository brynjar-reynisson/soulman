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
	db, err := NewDB(ctx, dbURL, "memory_test")
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
