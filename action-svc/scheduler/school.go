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

// RunOnce sends the due-or-overdue reminders, grouping same-date events
// that carry no specific time (or the same specific time) into one
// aggregated Discord message and one aggregated Calendar invite — school
// mail routinely produces two separate emails about the same occasion
// (e.g. two announcements about the same jersey day), and a parent wants
// one notification for that day, not two. Events on the same date but at
// genuinely different specific times stay separate, since those are
// different occasions. Each event's Discord and Calendar resolution is
// still tracked independently per-event (schoolevents.Event), so a
// partial send — some events in a group already resolved, others not —
// only re-sends the still-pending ones on the next run.
func (s *SchoolEventScheduler) RunOnce() {
	s.mu.Lock()
	defer s.mu.Unlock()

	due, err := schoolevents.DueOrOverdue(s.root, s.Now())
	if err != nil {
		slog.Error("scheduler: school events lookup failed", "error", err)
		return
	}
	for _, group := range groupByDateAndTime(due) {
		s.notifyGroup(group)
	}
}

// groupByDateAndTime groups events sharing the same Date and Time
// (date-only events all carry Time == "", so they group together
// regardless of description). Order of first appearance is preserved so
// output is deterministic given DueOrOverdue's own (directory-listing)
// order.
func groupByDateAndTime(events []schoolevents.Event) [][]schoolevents.Event {
	var order []string
	groups := map[string][]schoolevents.Event{}
	for _, e := range events {
		key := e.Date + "|" + e.Time
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], e)
	}
	result := make([][]schoolevents.Event, 0, len(order))
	for _, key := range order {
		result = append(result, groups[key])
	}
	return result
}

func (s *SchoolEventScheduler) notifyGroup(group []schoolevents.Event) {
	pendingDiscord := filterByDiscordPending(group)
	if len(pendingDiscord) > 0 {
		if err := s.sendDiscordWithRetry(formatSchoolMessage(pendingDiscord, s.Now())); err != nil {
			slog.Error("scheduler: school event discord send failed, will retry", "date", group[0].Date, "error", err)
		} else {
			for _, e := range pendingDiscord {
				if markErr := schoolevents.MarkDiscordSent(s.root, e.ID); markErr != nil {
					slog.Error("scheduler: mark discord sent failed", "id", e.ID, "error", markErr)
				}
			}
		}
	}

	pendingCalendar := filterByCalendarPending(group)
	if len(pendingCalendar) == 0 {
		return
	}
	if len(s.recipients) == 0 || s.inviter == nil {
		return // stays pending — a recipient/credentials added later still catches this up
	}
	inv := s.buildAggregatedInvite(pendingCalendar)
	if err := s.inviter.CreateInvite(context.Background(), inv); err != nil {
		slog.Error("scheduler: school event calendar invite failed, will retry", "date", group[0].Date, "error", err)
		return
	}
	for _, e := range pendingCalendar {
		if err := schoolevents.MarkCalendarStatus(s.root, e.ID, "sent"); err != nil {
			slog.Error("scheduler: mark calendar sent failed", "id", e.ID, "error", err)
		}
	}
}

func filterByDiscordPending(group []schoolevents.Event) []schoolevents.Event {
	var out []schoolevents.Event
	for _, e := range group {
		if e.DiscordStatus == "pending" {
			out = append(out, e)
		}
	}
	return out
}

func filterByCalendarPending(group []schoolevents.Event) []schoolevents.Event {
	var out []schoolevents.Event
	for _, e := range group {
		if e.CalendarStatus == "pending" {
			out = append(out, e)
		}
	}
	return out
}

// buildAggregatedInvite combines every event in group (all sharing the
// same Date/HasTime/Time, per groupByDateAndTime) into a single Calendar
// event: descriptions joined into the summary, distinct contacts joined
// into the description — different events in the same group can carry
// different contacts (e.g. two teachers' emails), which is exactly the
// signal for which child each part of the invite applies to.
func (s *SchoolEventScheduler) buildAggregatedInvite(group []schoolevents.Event) calendar.Invite {
	first := group[0]
	summaries := make([]string, len(group))
	var contacts []string
	seenContact := map[string]bool{}
	for i, e := range group {
		summaries[i] = e.Description
		contact := contactOrSender(e)
		if !seenContact[contact] {
			seenContact[contact] = true
			contacts = append(contacts, contact)
		}
	}
	return calendar.Invite{
		Summary:     strings.Join(summaries, "; "),
		Description: "from " + strings.Join(contacts, ", "),
		Date:        first.Date,
		HasTime:     first.HasTime,
		Time:        first.Time,
		Attendees:   s.recipients,
	}
}

// contactOrSender prefers an event's extracted ContactEmail (a specific
// teacher's real address) over its Sender (often a generic no-reply
// system address) — the contact is the much stronger "which child is
// this about" signal when grades aren't specified.
func contactOrSender(e schoolevents.Event) string {
	if e.ContactEmail != "" {
		return e.ContactEmail
	}
	return e.Sender
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

// formatSchoolMessage builds one Discord message for a group of events
// sharing the same date/time (see groupByDateAndTime) — a single-event
// group renders as a one-item list. Labeled "Soulman Reminder" to
// visually separate it, in the shared Discord channel, from the regular
// daily-report/gmail-triage messages. The date label reflects the actual
// relation to now (Today/Tomorrow/a literal date) rather than always
// saying "Tomorrow" — the startup catch-up run can surface an
// already-due or same-day event, not just a strictly-tomorrow one.
func formatSchoolMessage(group []schoolevents.Event, now time.Time) string {
	first := group[0]
	when := relativeDayLabel(first.Date, now)
	if first.HasTime {
		when += " " + first.Time
	}

	var b strings.Builder
	b.WriteString("**Soulman Reminder**\n📅 ")
	b.WriteString(when)
	b.WriteString(":\n")
	for _, e := range group {
		b.WriteString("• ")
		b.WriteString(e.Description)
		b.WriteString(" (from ")
		b.WriteString(contactOrSender(e))
		b.WriteString(")\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// relativeDayLabel returns "Today (<date>)"/"Tomorrow (<date>)" when date
// is today or tomorrow relative to now, else the literal date. Falls back
// to the literal string on a parse failure rather than erroring — this is
// display-only, never load-bearing for the due/overdue decision itself
// (that's DueOrOverdue's job).
func relativeDayLabel(date string, now time.Time) string {
	parsed, err := time.ParseInLocation("2006-01-02", date, now.Location())
	if err != nil {
		return date
	}
	today := startOfDay(now)
	switch {
	case parsed.Equal(today):
		return "Today (" + date + ")"
	case parsed.Equal(today.AddDate(0, 0, 1)):
		return "Tomorrow (" + date + ")"
	default:
		return date
	}
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
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
