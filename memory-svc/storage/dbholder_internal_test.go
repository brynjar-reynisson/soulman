package storage

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"soulman/common/dephealth"
)

// These tests exercise recordDBOutcome directly, so this file lives in
// package storage (white-box) rather than storage_test (black-box) —
// mirroring reconnect_test.go's existing precedent in this same
// directory for tests that need access to unexported helpers.
// dbholder_test.go stays storage_test for the delegate-method
// black-box tests, which don't need this access.

func TestRecordDBOutcome_NilErr_RecordsOK(t *testing.T) {
	reg := dephealth.NewRegistry()

	recordDBOutcome(reg, "postgres", nil)

	if st := reg.Snapshot()["postgres"]; st.State != "ok" {
		t.Errorf("State = %q, want ok", st.State)
	}
}

func TestRecordDBOutcome_PgError_RecordsOK_NotDown(t *testing.T) {
	reg := dephealth.NewRegistry()
	// Seed as ok so we can tell a query-level error doesn't flip it down.
	recordDBOutcome(reg, "postgres", nil)

	pgErr := &pgconn.PgError{Code: "42P01", Message: "relation \"raw_inputs\" does not exist"}
	recordDBOutcome(reg, "postgres", pgErr)

	if st := reg.Snapshot()["postgres"]; st.State != "ok" {
		t.Errorf("State = %q, want ok — a *pgconn.PgError proves the connection is reachable", st.State)
	}
}

func TestRecordDBOutcome_ErrNoRows_RecordsOK_NotDown(t *testing.T) {
	reg := dephealth.NewRegistry()
	recordDBOutcome(reg, "postgres", nil)

	recordDBOutcome(reg, "postgres", pgx.ErrNoRows)

	if st := reg.Snapshot()["postgres"]; st.State != "ok" {
		t.Errorf("State = %q, want ok — pgx.ErrNoRows proves the connection is reachable", st.State)
	}
}

func TestRecordDBOutcome_ConnectivityError_RecordsDown(t *testing.T) {
	reg := dephealth.NewRegistry()
	recordDBOutcome(reg, "postgres", nil)

	recordDBOutcome(reg, "postgres", errors.New("connection refused"))

	if st := reg.Snapshot()["postgres"]; st.State != "down" {
		t.Errorf("State = %q, want down — a plain connectivity error should latch down", st.State)
	}
}

func TestRecordDBOutcome_WrappedPgError_RecordsOK_NotDown(t *testing.T) {
	reg := dephealth.NewRegistry()
	recordDBOutcome(reg, "postgres", nil)

	pgErr := &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
	wrapped := fmt.Errorf("insert failed: %w", pgErr)
	recordDBOutcome(reg, "postgres", wrapped)

	if st := reg.Snapshot()["postgres"]; st.State != "ok" {
		t.Errorf("State = %q, want ok — a wrapped *pgconn.PgError should still be detected via errors.As", st.State)
	}
}
