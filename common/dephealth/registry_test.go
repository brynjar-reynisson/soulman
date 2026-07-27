package dephealth_test

import (
	"errors"
	"testing"

	"soulman/common/dephealth"
)

func TestRegistry_Record_NilError_SetsOK(t *testing.T) {
	r := dephealth.NewRegistry()
	r.Record("postgres", nil)

	snap := r.Snapshot()
	st, ok := snap["postgres"]
	if !ok {
		t.Fatal(`Snapshot()["postgres"] missing after Record(nil)`)
	}
	if st.State != "ok" {
		t.Errorf("State = %q, want ok", st.State)
	}
	if st.Detail != "" {
		t.Errorf("Detail = %q, want empty", st.Detail)
	}
}

func TestRegistry_Record_Error_SetsDown(t *testing.T) {
	r := dephealth.NewRegistry()
	r.Record("postgres", errors.New("connection refused"))

	st := r.Snapshot()["postgres"]
	if st.State != "down" {
		t.Errorf("State = %q, want down", st.State)
	}
	if st.Detail != "connection refused" {
		t.Errorf("Detail = %q, want %q", st.Detail, "connection refused")
	}
	if st.Since.IsZero() {
		t.Error("Since is zero, want a timestamp")
	}
}

func TestRegistry_Record_RepeatedSameState_DoesNotResetSince(t *testing.T) {
	r := dephealth.NewRegistry()
	r.Record("postgres", errors.New("first failure"))
	firstSince := r.Snapshot()["postgres"].Since

	r.Record("postgres", errors.New("second failure, still down"))
	second := r.Snapshot()["postgres"]

	if !second.Since.Equal(firstSince) {
		t.Errorf("Since changed on repeated down->down Record: got %v, want unchanged %v", second.Since, firstSince)
	}
	if second.Detail != "second failure, still down" {
		t.Errorf("Detail = %q, want the latest error's text (%q)", second.Detail, "second failure, still down")
	}
}

func TestRegistry_Record_Transition_UpdatesSince(t *testing.T) {
	r := dephealth.NewRegistry()
	r.Record("postgres", errors.New("down"))
	downSince := r.Snapshot()["postgres"].Since

	r.Record("postgres", nil)
	okStatus := r.Snapshot()["postgres"]

	if okStatus.State != "ok" {
		t.Fatalf("State = %q, want ok", okStatus.State)
	}
	if okStatus.Since.Equal(downSince) {
		t.Error("Since did not change on a down->ok transition")
	}
}

func TestRegistry_Snapshot_Empty_ReturnsEmptyMap(t *testing.T) {
	r := dephealth.NewRegistry()
	snap := r.Snapshot()
	if len(snap) != 0 {
		t.Errorf("Snapshot() = %v, want empty map", snap)
	}
}

func TestRegistry_Snapshot_MultipleNames_Independent(t *testing.T) {
	r := dephealth.NewRegistry()
	r.Record("postgres", nil)
	r.Record("discord", errors.New("webhook timeout"))

	snap := r.Snapshot()
	if snap["postgres"].State != "ok" {
		t.Errorf("postgres.State = %q, want ok", snap["postgres"].State)
	}
	if snap["discord"].State != "down" {
		t.Errorf("discord.State = %q, want down", snap["discord"].State)
	}
}

func TestRegistry_Snapshot_ReturnsCopy_NotLiveMap(t *testing.T) {
	r := dephealth.NewRegistry()
	r.Record("postgres", nil)

	snap := r.Snapshot()
	snap["postgres"] = dephealth.Status{State: "down"}

	fresh := r.Snapshot()
	if fresh["postgres"].State != "ok" {
		t.Error("mutating a returned Snapshot() map leaked into the registry's internal state")
	}
}
