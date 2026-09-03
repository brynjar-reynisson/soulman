package scheduler

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"soulman/action-svc/calendar"
	"soulman/action-svc/notify"
	"soulman/action-svc/schoolevents"
)

// EventInviter is satisfied by *calendar.Client. Defined here (not
// re-exported from calendar) purely so SchoolEventScheduler's tests can
// inject a fake without a live Google account — mirrors OutcomePublisher's
// same-file interface-definition convention above.
type EventInviter interface {
	CreateInvite(ctx context.Context, inv calendar.Invite) error
}

// SchoolEventScheduler wakes at notifyTime each day, plus once immediately
// on Start (the "better late than never" catch-up — see the design spec),
// and for every due-or-overdue pending event attempts each still-pending
// channel independently: a Discord message to the owner (discordNotifier —
// deliberately a plain, non-feign-wrapped notifier; see the design spec's
// explicit divergence), and — only when recipients is non-empty — a
// Calendar invite via inviter. Same wake-loop shape as Scheduler and
// dnd.Flusher (nextRun/time.After, overridable Now/BackoffBase) — the wait
// duration is computed from s.Now() directly rather than via time.Until,
// so a mocked Now in tests can't desync from the real wall clock (see loop).
type SchoolEventScheduler struct {
	root            string
	notifyTime      string
	recipients      []string
	discordNotifier notify.Notifier
	inviter         EventInviter
	stop            chan struct{}

	// mu serializes RunOnce so the immediate Start() catch-up and the wake
	// loop's first tick (which can land almost back-to-back if the process
	// starts shortly before notifyTime) never race on schoolevents' file
	// operations.
	mu sync.Mutex

	Now         func() time.Time
	BackoffBase time.Duration
}

func NewSchoolEventScheduler(root, notifyTime string, recipients []string, discordNotifier notify.Notifier, inviter EventInviter) *SchoolEventScheduler {
	return &SchoolEventScheduler{
		root:            root,
		notifyTime:      notifyTime,
		recipients:      recipients,
		discordNotifier: discordNotifier,
		inviter:         inviter,
		stop:            make(chan struct{}),
		Now:             time.Now,
		BackoffBase:     1 * time.Second,
	}
}

// Start launches the catch-up check (RunOnce, immediately — anything due
// or overdue right now, including a missed tick from downtime, fires
// without waiting) and the daily wake loop, both non-blocking, mirroring
// dnd.Flusher.Start exactly.
func (s *SchoolEventScheduler) Start() {
	go s.RunOnce()
	go s.loop()
}

func (s *SchoolEventScheduler) Stop() {
	close(s.stop)
}

func (s *SchoolEventScheduler) loop() {
	for {
		// Computed from s.Now() rather than time.Until (which measures
		// against the real wall clock) so a mocked Now — used by
		// TestStart_CatchUp_FiresImmediatelyWithoutWaitingForTick — can't
		// produce a spuriously negative/near-zero wait that races the
		// catch-up goroutine into firing twice. Identical to time.Until in
		// production, where Now is time.Now.
		from := s.Now()
		wait := s.nextRun(from).Sub(from)
		select {
		case <-time.After(wait):
			s.RunOnce()
		case <-s.stop:
			return
		}
	}
}

func (s *SchoolEventScheduler) nextRun(from time.Time) time.Time {
	hh, mm := parseSchoolSendTime(s.notifyTime)
	next := time.Date(from.Year(), from.Month(), from.Day(), hh, mm, 0, 0, from.Location())
	if !next.After(from) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// RunOnce sends the due-or-overdue reminders for each still-pending
// channel. Each event's Discord and Calendar attempts are independent: a
// failure in one never blocks or re-triggers the other.
func (s *SchoolEventScheduler) RunOnce() {
	s.mu.Lock()
	defer s.mu.Unlock()

	due, err := schoolevents.DueOrOverdue(s.root, s.Now())
	if err != nil {
		slog.Error("scheduler: school events lookup failed", "error", err)
		return
	}
	for _, e := range due {
		s.notifyOne(e)
	}
}

func (s *SchoolEventScheduler) notifyOne(e schoolevents.Event) {
	if e.DiscordStatus == "pending" {
		if err := s.sendDiscordWithRetry(formatSchoolMessage(e)); err != nil {
			slog.Error("scheduler: school event discord send failed, will retry", "id", e.ID, "error", err)
		} else if markErr := schoolevents.MarkDiscordSent(s.root, e.ID); markErr != nil {
			slog.Error("scheduler: mark discord sent failed", "id", e.ID, "error", markErr)
		}
	}

	if e.CalendarStatus != "pending" {
		return
	}
	if len(s.recipients) == 0 || s.inviter == nil {
		return // stays pending — a recipient/credentials added later still catches this up
	}
	inv := calendar.Invite{
		Summary:     e.Description,
		Description: "from " + e.Sender,
		Date:        e.Date,
		HasTime:     e.HasTime,
		Time:        e.Time,
		Attendees:   s.recipients,
	}
	if err := s.inviter.CreateInvite(context.Background(), inv); err != nil {
		slog.Error("scheduler: school event calendar invite failed, will retry", "id", e.ID, "error", err)
		return
	}
	if err := schoolevents.MarkCalendarStatus(s.root, e.ID, "sent"); err != nil {
		slog.Error("scheduler: mark calendar sent failed", "id", e.ID, "error", err)
	}
}

func (s *SchoolEventScheduler) sendDiscordWithRetry(message string) error {
	var err error
	backoff := s.BackoffBase
	for attempt := 1; attempt <= 3; attempt++ {
		err = s.discordNotifier.Send(message)
		if err == nil {
			return nil
		}
		slog.Warn("scheduler: notifier send attempt failed", "attempt", attempt, "max_attempts", 3, "error", err)
		if attempt < 3 {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return err
}

func formatSchoolMessage(e schoolevents.Event) string {
	when := e.Date
	if e.HasTime {
		when += " " + e.Time
	}
	return "📅 Tomorrow: " + e.Description + " (" + when + ", from " + e.Sender + ")"
}

func parseSchoolSendTime(s string) (int, int) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 16, 0
	}
	hh, err1 := strconv.Atoi(parts[0])
	mm, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 16, 0
	}
	return hh, mm
}
