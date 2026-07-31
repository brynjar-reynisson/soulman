package storage_test

import (
	"context"
	"errors"
	"testing"

	"soulman/common/dephealth"
	"soulman/memory-svc/storage"
)

func TestDBHolder_NewDBHolder_NilDB_GetReturnsNil(t *testing.T) {
	reg := dephealth.NewRegistry()
	h := storage.NewDBHolder(nil, reg)

	if h.Get() != nil {
		t.Error("Get() = non-nil, want nil for a holder constructed with nil db")
	}
}

func TestDBHolder_NewDBHolder_NilDB_RecordsDown(t *testing.T) {
	reg := dephealth.NewRegistry()
	storage.NewDBHolder(nil, reg)

	st, ok := reg.Snapshot()["postgres"]
	if !ok {
		t.Fatal(`Snapshot()["postgres"] missing after NewDBHolder(nil, ...)`)
	}
	if st.State != "down" {
		t.Errorf("State = %q, want down", st.State)
	}
}

func TestDBHolder_NewDBHolder_ConnectedDB_RecordsOK(t *testing.T) {
	db := testDB(t) // skips if Postgres unavailable
	reg := dephealth.NewRegistry()
	h := storage.NewDBHolder(db, reg)

	if h.Get() != db {
		t.Error("Get() did not return the db passed to NewDBHolder")
	}
	if reg.Snapshot()["postgres"].State != "ok" {
		t.Errorf("State = %q, want ok", reg.Snapshot()["postgres"].State)
	}
}

func TestDBHolder_InsertRawInput_NotConnected_ReturnsErrNotConnected(t *testing.T) {
	h := storage.NewDBHolder(nil, dephealth.NewRegistry())

	err := h.InsertRawInput(context.Background(), nil)
	if !errors.Is(err, storage.ErrNotConnected) {
		t.Errorf("err = %v, want ErrNotConnected", err)
	}
}

func TestDBHolder_GetRecent_NotConnected_ReturnsErrNotConnected(t *testing.T) {
	h := storage.NewDBHolder(nil, dephealth.NewRegistry())

	_, err := h.GetRecent(context.Background(), 5)
	if !errors.Is(err, storage.ErrNotConnected) {
		t.Errorf("err = %v, want ErrNotConnected", err)
	}
}

func TestDBHolder_GetRecentEpisodes_NotConnected_ReturnsErrNotConnected(t *testing.T) {
	h := storage.NewDBHolder(nil, dephealth.NewRegistry())

	_, err := h.GetRecentEpisodes(context.Background(), 5)
	if !errors.Is(err, storage.ErrNotConnected) {
		t.Errorf("err = %v, want ErrNotConnected", err)
	}
}

func TestDBHolder_WriteEpisode_NotConnected_ReturnsErrNotConnected(t *testing.T) {
	h := storage.NewDBHolder(nil, dephealth.NewRegistry())

	err := h.WriteEpisode(context.Background(), 1, nil)
	if !errors.Is(err, storage.ErrNotConnected) {
		t.Errorf("err = %v, want ErrNotConnected", err)
	}
}

func TestDBHolder_Close_NilDB_DoesNotPanic(t *testing.T) {
	h := storage.NewDBHolder(nil, dephealth.NewRegistry())
	h.Close() // must not panic
}

func TestDBHolder_Close_ConnectedDB_GetReturnsNilAfter(t *testing.T) {
	db := testDB(t) // skips if Postgres unavailable
	h := storage.NewDBHolder(db, dephealth.NewRegistry())

	h.Close()

	if h.Get() != nil {
		t.Error("Get() = non-nil after Close(), want nil")
	}
}
