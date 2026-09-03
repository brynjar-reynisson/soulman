package calendar

import (
	"testing"
)

func TestToCalendarEvent_DateOnly_AllDayEvent(t *testing.T) {
	inv := Invite{Summary: "Sweater day", Date: "2026-09-04", HasTime: false, Attendees: []string{"a@example.com"}}
	ev := toCalendarEvent(inv)

	if ev.Start.Date != "2026-09-04" {
		t.Errorf("Start.Date = %q, want 2026-09-04", ev.Start.Date)
	}
	if ev.End.Date != "2026-09-05" {
		t.Errorf("End.Date = %q, want 2026-09-05 (exclusive end)", ev.End.Date)
	}
	if ev.Start.DateTime != "" || ev.End.DateTime != "" {
		t.Error("DateTime fields must be empty for an all-day event")
	}
}

func TestToCalendarEvent_WithTime_OneHourDuration(t *testing.T) {
	inv := Invite{Summary: "Parent meeting", Date: "2026-09-04", HasTime: true, Time: "14:00", Attendees: []string{"a@example.com"}}
	ev := toCalendarEvent(inv)

	if ev.Start.DateTime == "" || ev.End.DateTime == "" {
		t.Fatal("DateTime fields must be set for a timed event")
	}
	if ev.Start.Date != "" || ev.End.Date != "" {
		t.Error("Date fields must be empty for a timed event")
	}
}

func TestToCalendarEvent_AttendeesMapped(t *testing.T) {
	inv := Invite{Summary: "x", Date: "2026-09-04", Attendees: []string{"a@example.com", "b@example.com"}}
	ev := toCalendarEvent(inv)

	if len(ev.Attendees) != 2 || ev.Attendees[0].Email != "a@example.com" || ev.Attendees[1].Email != "b@example.com" {
		t.Errorf("Attendees = %+v, want a@example.com and b@example.com", ev.Attendees)
	}
}

func TestToCalendarEvent_SummaryAndDescriptionSet(t *testing.T) {
	inv := Invite{Summary: "Sweater day", Description: "from teacher@reykjavik.is", Date: "2026-09-04"}
	ev := toCalendarEvent(inv)

	if ev.Summary != "Sweater day" || ev.Description != "from teacher@reykjavik.is" {
		t.Errorf("Summary/Description = %q/%q, want Sweater day / from teacher@reykjavik.is", ev.Summary, ev.Description)
	}
}
