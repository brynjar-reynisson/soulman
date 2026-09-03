package scheduler_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"soulman/action-svc/calendar"
	"soulman/action-svc/schoolevents"
	"soulman/action-svc/scheduler"
)

type fakeDiscordNotifier struct {
	mu       sync.Mutex
	messages []string
	err      error
}

func (f *fakeDiscordNotifier) Send(message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.messages = append(f.messages, message)
	return nil
}
func (f *fakeDiscordNotifier) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.messages...)
}

type fakeInviter struct {
	mu      sync.Mutex
	invites []calendar.Invite
	err     error
}

func (f *fakeInviter) CreateInvite(_ context.Context, inv calendar.Invite) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.invites = append(f.invites, inv)
	return nil
}
func (f *fakeInviter) created() []calendar.Invite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]calendar.Invite(nil), f.invites...)
}

func TestRunOnce_DueEvent_SendsDiscordAndCalendar_MarksBothSent(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 16, 0, 0, 0, time.Local)
	id := schoolevents.ID("t1", 0, "2026-09-04")
	schoolevents.Save(root, schoolevents.Event{ID: id, Date: "2026-09-04", Description: "Sweater day", DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now})

	discord := &fakeDiscordNotifier{}
	inviter := &fakeInviter{}
	s := scheduler.NewSchoolEventScheduler(root, "16:00", []string{"her@example.com"}, discord, inviter)
	s.Now = func() time.Time { return now }
	s.RunOnce()

	if len(discord.sent()) != 1 {
		t.Errorf("discord messages = %v, want 1", discord.sent())
	}
	if len(inviter.created()) != 1 || inviter.created()[0].Attendees[0] != "her@example.com" {
		t.Errorf("invites = %v, want 1 to her@example.com", inviter.created())
	}

	due, _ := schoolevents.DueOrOverdue(root, now)
	if len(due) != 0 {
		t.Errorf("DueOrOverdue after RunOnce = %v, want empty (both channels resolved)", due)
	}
}

func TestRunOnce_EmptyRecipients_SkipsCalendarNotDiscord(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 16, 0, 0, 0, time.Local)
	id := schoolevents.ID("t1", 0, "2026-09-04")
	schoolevents.Save(root, schoolevents.Event{ID: id, Date: "2026-09-04", DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now})

	discord := &fakeDiscordNotifier{}
	inviter := &fakeInviter{}
	s := scheduler.NewSchoolEventScheduler(root, "16:00", nil, discord, inviter)
	s.Now = func() time.Time { return now }
	s.RunOnce()

	if len(discord.sent()) != 1 {
		t.Errorf("discord messages = %v, want 1", discord.sent())
	}
	if len(inviter.created()) != 0 {
		t.Errorf("invites = %v, want 0 when recipients is empty", inviter.created())
	}

	// Calendar stays pending (not skipped) so a recipient added later still
	// catches this event up.
	due, _ := schoolevents.DueOrOverdue(root, now)
	if len(due) != 1 || due[0].CalendarStatus != "pending" {
		t.Errorf("DueOrOverdue = %v, want 1 entry with CalendarStatus still pending", due)
	}
}

func TestRunOnce_DiscordFails_CalendarStillAttempted_DiscordStaysPending(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 16, 0, 0, 0, time.Local)
	id := schoolevents.ID("t1", 0, "2026-09-04")
	schoolevents.Save(root, schoolevents.Event{ID: id, Date: "2026-09-04", DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now})

	discord := &fakeDiscordNotifier{err: context.DeadlineExceeded}
	inviter := &fakeInviter{}
	s := scheduler.NewSchoolEventScheduler(root, "16:00", []string{"her@example.com"}, discord, inviter)
	s.Now = func() time.Time { return now }
	s.BackoffBase = time.Millisecond
	s.RunOnce()

	if len(inviter.created()) != 1 {
		t.Errorf("invites = %v, want 1 — calendar attempted independently of discord's failure", inviter.created())
	}

	due, _ := schoolevents.DueOrOverdue(root, now)
	if len(due) != 1 || due[0].DiscordStatus != "pending" || due[0].CalendarStatus != "sent" {
		t.Errorf("DueOrOverdue = %v, want DiscordStatus=pending CalendarStatus=sent", due)
	}
}

func TestRunOnce_NilInviter_StaysPending(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 16, 0, 0, 0, time.Local)
	id := schoolevents.ID("t1", 0, "2026-09-04")
	schoolevents.Save(root, schoolevents.Event{ID: id, Date: "2026-09-04", DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now})

	discord := &fakeDiscordNotifier{}
	// recipients is non-empty but inviter is nil — the state main.go
	// produces when CALENDAR_CLIENT_ID/SECRET/REFRESH_TOKEN aren't yet
	// configured even though calendar_recipient_emails is. Must not panic.
	s := scheduler.NewSchoolEventScheduler(root, "16:00", []string{"her@example.com"}, discord, nil)
	s.Now = func() time.Time { return now }
	s.RunOnce()

	due, _ := schoolevents.DueOrOverdue(root, now)
	if len(due) != 1 || due[0].CalendarStatus != "pending" {
		t.Errorf("DueOrOverdue = %v, want CalendarStatus still pending with a nil inviter", due)
	}
}

func TestRunOnce_AlreadyResolvedChannel_NotRetried(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 16, 0, 0, 0, time.Local)
	id := schoolevents.ID("t1", 0, "2026-09-04")
	schoolevents.Save(root, schoolevents.Event{ID: id, Date: "2026-09-04", DiscordStatus: "sent", CalendarStatus: "pending", CreatedAt: now})

	discord := &fakeDiscordNotifier{}
	inviter := &fakeInviter{}
	s := scheduler.NewSchoolEventScheduler(root, "16:00", []string{"her@example.com"}, discord, inviter)
	s.Now = func() time.Time { return now }
	s.RunOnce()

	if len(discord.sent()) != 0 {
		t.Errorf("discord messages = %v, want 0 — already sent", discord.sent())
	}
	if len(inviter.created()) != 1 {
		t.Errorf("invites = %v, want 1 — calendar was still pending", inviter.created())
	}
}

func TestRunOnce_TwoDateOnlyEventsSameDate_AggregatedIntoOneMessageAndOneInvite(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 16, 0, 0, 0, time.Local)
	id1 := schoolevents.ID("t1", 0, "2026-09-04")
	id2 := schoolevents.ID("t2", 0, "2026-09-04")
	schoolevents.Save(root, schoolevents.Event{ID: id1, Date: "2026-09-04", Description: "Treyjudagur - wear a shirt", Sender: "noreply@mentor.is", DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now})
	schoolevents.Save(root, schoolevents.Event{ID: id2, Date: "2026-09-04", Description: "Treyjudagurinn - wear jerseys", Sender: "noreply@mentor.is", DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now})

	discord := &fakeDiscordNotifier{}
	inviter := &fakeInviter{}
	s := scheduler.NewSchoolEventScheduler(root, "16:00", []string{"her@example.com"}, discord, inviter)
	s.Now = func() time.Time { return now }
	s.RunOnce()

	if len(discord.sent()) != 1 {
		t.Fatalf("discord messages = %v, want 1 aggregated message for two same-date events", discord.sent())
	}
	msg := discord.sent()[0]
	if !strings.Contains(msg, "Treyjudagur - wear a shirt") || !strings.Contains(msg, "Treyjudagurinn - wear jerseys") {
		t.Errorf("aggregated message = %q, want both descriptions present", msg)
	}
	if !strings.Contains(msg, "Soulman Reminder") {
		t.Errorf("aggregated message = %q, want the Soulman Reminder label", msg)
	}

	if len(inviter.created()) != 1 {
		t.Fatalf("invites = %v, want 1 aggregated invite for two same-date events", inviter.created())
	}
	inv := inviter.created()[0]
	if !strings.Contains(inv.Summary, "Treyjudagur - wear a shirt") || !strings.Contains(inv.Summary, "Treyjudagurinn - wear jerseys") {
		t.Errorf("invite.Summary = %q, want both descriptions present", inv.Summary)
	}

	due, _ := schoolevents.DueOrOverdue(root, now)
	if len(due) != 0 {
		t.Errorf("DueOrOverdue after RunOnce = %v, want empty (both events fully resolved)", due)
	}
}

func TestRunOnce_SameDateDifferentTimes_SentAsSeparateMessagesAndInvites(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 16, 0, 0, 0, time.Local)
	id1 := schoolevents.ID("t1", 0, "2026-09-04")
	id2 := schoolevents.ID("t2", 0, "2026-09-04")
	schoolevents.Save(root, schoolevents.Event{ID: id1, Date: "2026-09-04", HasTime: true, Time: "14:00", Description: "5th grade meeting", DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now})
	schoolevents.Save(root, schoolevents.Event{ID: id2, Date: "2026-09-04", HasTime: true, Time: "16:00", Description: "8th grade meeting", DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now})

	discord := &fakeDiscordNotifier{}
	inviter := &fakeInviter{}
	s := scheduler.NewSchoolEventScheduler(root, "16:00", []string{"her@example.com"}, discord, inviter)
	s.Now = func() time.Time { return now }
	s.RunOnce()

	if len(discord.sent()) != 2 {
		t.Errorf("discord messages = %v, want 2 separate messages for two different times on the same date", discord.sent())
	}
	if len(inviter.created()) != 2 {
		t.Errorf("invites = %v, want 2 separate invites for two different times on the same date", inviter.created())
	}
}

func TestRunOnce_AggregatedGroup_PartialCalendarFailure_OnlyFailedOnesStayPending(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 16, 0, 0, 0, time.Local)
	id1 := schoolevents.ID("t1", 0, "2026-09-04")
	id2 := schoolevents.ID("t2", 0, "2026-09-04")
	schoolevents.Save(root, schoolevents.Event{ID: id1, Date: "2026-09-04", Description: "A", DiscordStatus: "sent", CalendarStatus: "pending", CreatedAt: now})
	schoolevents.Save(root, schoolevents.Event{ID: id2, Date: "2026-09-04", Description: "B", DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now})

	discord := &fakeDiscordNotifier{}
	inviter := &fakeInviter{}
	s := scheduler.NewSchoolEventScheduler(root, "16:00", []string{"her@example.com"}, discord, inviter)
	s.Now = func() time.Time { return now }
	s.RunOnce()

	// Only the still-pending event (B) is included in the Discord send,
	// since A's Discord channel was already resolved.
	if len(discord.sent()) != 1 || !strings.Contains(discord.sent()[0], "B") || strings.Contains(discord.sent()[0], "A") {
		t.Errorf("discord messages = %v, want 1 message containing only B (A already sent)", discord.sent())
	}
	// Both A and B are still Calendar-pending, so the aggregated invite
	// covers both.
	if len(inviter.created()) != 1 {
		t.Fatalf("invites = %v, want 1 aggregated invite covering both A and B", inviter.created())
	}
}

func TestStart_CatchUp_FiresImmediatelyWithoutWaitingForTick(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 3, 8, 0, 0, 0, time.Local) // well before 16:00
	id := schoolevents.ID("t1", 0, "2026-09-03")          // due today (overdue relative to a missed run)
	schoolevents.Save(root, schoolevents.Event{ID: id, Date: "2026-09-03", DiscordStatus: "pending", CalendarStatus: "skipped", CreatedAt: now})

	discord := &fakeDiscordNotifier{}
	inviter := &fakeInviter{}
	s := scheduler.NewSchoolEventScheduler(root, "16:00", nil, discord, inviter)
	s.Now = func() time.Time { return now }
	s.Start()
	defer s.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(discord.sent()) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("discord messages = %v after 2s, want 1 from the startup catch-up check", discord.sent())
}
