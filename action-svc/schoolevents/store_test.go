package schoolevents_test

import (
	"path/filepath"
	"testing"
	"time"

	"soulman/action-svc/schoolevents"
)

func TestID_Deterministic(t *testing.T) {
	id1 := schoolevents.ID("thread-1", 0, "2026-09-04")
	id2 := schoolevents.ID("thread-1", 0, "2026-09-04")
	if id1 != id2 {
		t.Errorf("ID = %q and %q, want identical for the same inputs", id1, id2)
	}
}

func TestID_DiffersByIndex(t *testing.T) {
	id1 := schoolevents.ID("thread-1", 0, "2026-09-04")
	id2 := schoolevents.ID("thread-1", 1, "2026-09-04")
	if id1 == id2 {
		t.Error("ID should differ for different event indexes within the same thread")
	}
}

func TestSave_And_DueOrOverdue_ReturnsPendingEvent(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.Local)
	e := schoolevents.Event{
		ID: schoolevents.ID("t1", 0, "2026-09-04"), Date: "2026-09-04",
		Description: "Sweater day", Sender: "teacher@reykjavik.is",
		DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now,
	}
	if err := schoolevents.Save(root, e); err != nil {
		t.Fatalf("Save: %v", err)
	}

	due, err := schoolevents.DueOrOverdue(root, now)
	if err != nil {
		t.Fatalf("DueOrOverdue: %v", err)
	}
	if len(due) != 1 || due[0].ID != e.ID {
		t.Errorf("DueOrOverdue = %v, want 1 entry matching %s", due, e.ID)
	}
}

func TestDueOrOverdue_FutureEventBeyondTomorrow_NotReturned(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.Local)
	e := schoolevents.Event{
		ID: schoolevents.ID("t1", 0, "2026-09-20"), Date: "2026-09-20",
		DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now,
	}
	schoolevents.Save(root, e)

	due, err := schoolevents.DueOrOverdue(root, now)
	if err != nil {
		t.Fatalf("DueOrOverdue: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("DueOrOverdue = %v, want empty for an event more than a day out", due)
	}
}

func TestDueOrOverdue_StaleBeyondCutoff_NotReturned(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.Local)
	e := schoolevents.Event{
		ID: schoolevents.ID("t1", 0, "2026-09-04"), Date: "2026-09-04", // 6 days past
		DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now,
	}
	schoolevents.Save(root, e)

	due, err := schoolevents.DueOrOverdue(root, now)
	if err != nil {
		t.Fatalf("DueOrOverdue: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("DueOrOverdue = %v, want empty for an event more than 2 days stale", due)
	}
}

func TestDueOrOverdue_BoundaryAt2DaysStale_WithNonMidnightNow_IsIncluded(t *testing.T) {
	root := t.TempDir()
	// now is at noon, 2 calendar days past the event date.
	// This tests that date comparison is calendar-day-based, not wall-clock-based.
	// Event on 2026-09-08, now is 2026-09-10T12:00 — exactly at the 2-day boundary.
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.Local)
	e := schoolevents.Event{
		ID: schoolevents.ID("t1", 0, "2026-09-08"), Date: "2026-09-08",
		DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now,
	}
	schoolevents.Save(root, e)

	due, err := schoolevents.DueOrOverdue(root, now)
	if err != nil {
		t.Fatalf("DueOrOverdue: %v", err)
	}
	if len(due) != 1 || due[0].ID != e.ID {
		t.Errorf("DueOrOverdue = %v, want 1 entry at the 2-day stale boundary", due)
	}
}

func TestMarkDiscordSent_ExcludesFromFutureDueOrOverdue_WhenCalendarAlsoResolved(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.Local)
	id := schoolevents.ID("t1", 0, "2026-09-04")
	e := schoolevents.Event{ID: id, Date: "2026-09-04", DiscordStatus: "pending", CalendarStatus: "skipped", CreatedAt: now}
	schoolevents.Save(root, e)

	if err := schoolevents.MarkDiscordSent(root, id); err != nil {
		t.Fatalf("MarkDiscordSent: %v", err)
	}

	due, err := schoolevents.DueOrOverdue(root, now)
	if err != nil {
		t.Fatalf("DueOrOverdue: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("DueOrOverdue = %v, want empty once both channels are resolved", due)
	}
}

func TestMarkDiscordSent_StillReturned_WhenCalendarStillPending(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.Local)
	id := schoolevents.ID("t1", 0, "2026-09-04")
	schoolevents.Save(root, schoolevents.Event{ID: id, Date: "2026-09-04", DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now})
	schoolevents.MarkDiscordSent(root, id)

	due, err := schoolevents.DueOrOverdue(root, now)
	if err != nil {
		t.Fatalf("DueOrOverdue: %v", err)
	}
	if len(due) != 1 || due[0].DiscordStatus != "sent" || due[0].CalendarStatus != "pending" {
		t.Errorf("DueOrOverdue = %v, want 1 entry with DiscordStatus=sent CalendarStatus=pending", due)
	}
}

func TestMarkCalendarStatus_SetsStatus(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.Local)
	id := schoolevents.ID("t1", 0, "2026-09-04")
	schoolevents.Save(root, schoolevents.Event{ID: id, Date: "2026-09-04", DiscordStatus: "sent", CalendarStatus: "pending", CreatedAt: now})

	if err := schoolevents.MarkCalendarStatus(root, id, "sent"); err != nil {
		t.Fatalf("MarkCalendarStatus: %v", err)
	}

	due, _ := schoolevents.DueOrOverdue(root, now)
	if len(due) != 0 {
		t.Errorf("DueOrOverdue = %v, want empty once Calendar is also sent", due)
	}
}

func TestSave_Idempotent_DoesNotResurrectFullyResolvedEvent(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.Local)
	id := schoolevents.ID("t1", 0, "2026-09-04")
	schoolevents.Save(root, schoolevents.Event{ID: id, Date: "2026-09-04", DiscordStatus: "sent", CalendarStatus: "sent", CreatedAt: now})

	// Re-processing the same email (e.g. backfill run twice) must not flip
	// an already-fully-resolved event back to pending.
	schoolevents.Save(root, schoolevents.Event{ID: id, Date: "2026-09-04", DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now})

	due, err := schoolevents.DueOrOverdue(root, now)
	if err != nil {
		t.Fatalf("DueOrOverdue: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("DueOrOverdue = %v, want empty — Save must not resurrect a fully-resolved event", due)
	}
}

func TestSave_PreservesAlreadyResolvedStatus_OnReprocessing(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.Local)
	id := schoolevents.ID("t1", 0, "2026-09-04")
	schoolevents.Save(root, schoolevents.Event{ID: id, Date: "2026-09-04", DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now})

	if err := schoolevents.MarkDiscordSent(root, id); err != nil {
		t.Fatalf("MarkDiscordSent: %v", err)
	}

	// Re-processing the same email (JetStream redelivery, or a backfill
	// rerun) always calls Save with both channels "pending" again — this
	// must not reset the already-sent Discord status back to pending.
	if err := schoolevents.Save(root, schoolevents.Event{ID: id, Date: "2026-09-04", DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	due, err := schoolevents.DueOrOverdue(root, now)
	if err != nil {
		t.Fatalf("DueOrOverdue: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("DueOrOverdue = %v, want 1 entry (CalendarStatus still pending)", due)
	}
	if due[0].DiscordStatus != "sent" {
		t.Errorf("DiscordStatus = %q, want %q to be preserved across reprocessing", due[0].DiscordStatus, "sent")
	}
	if due[0].CalendarStatus != "pending" {
		t.Errorf("CalendarStatus = %q, want %q unaffected", due[0].CalendarStatus, "pending")
	}
}

func TestDueOrOverdue_MissingDirectory_ReturnsEmptyNotError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist-yet")
	due, err := schoolevents.DueOrOverdue(root, time.Now())
	if err != nil {
		t.Fatalf("DueOrOverdue: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("DueOrOverdue = %v, want empty when the store directory doesn't exist yet", due)
	}
}
