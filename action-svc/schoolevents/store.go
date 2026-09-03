// Package schoolevents persists pending school-date reminders as one JSON
// file per event under $root/logs/school-events/ — the same local-state
// tier as action-svc's existing dnd-pending.txt and feigned-actions.jsonl
// (not memory-svc/Postgres — this is action-svc's own scheduling state).
// See docs/superpowers/specs/2026-09-03-school-email-events-design.md.
package schoolevents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// staleCutoffDays is the "better late than never, not forever" bound: an
// event more than this many days past its own Date stops being returned by
// DueOrOverdue, even if a channel is still pending.
const staleCutoffDays = 2

// Event is one persisted school-date reminder. DiscordStatus and
// CalendarStatus track independently ("pending" | "sent", plus "skipped"
// for CalendarStatus only) so a partial failure, or a recipient list that
// starts empty and gets filled in later, never causes an already-succeeded
// channel to fire twice.
type Event struct {
	ID             string    `json:"id"`
	Date           string    `json:"date"` // "YYYY-MM-DD"
	HasTime        bool      `json:"has_time"`
	Time           string    `json:"time"`
	Description    string    `json:"description"`
	Sender         string    `json:"sender"`
	Subject        string    `json:"subject"`
	DiscordStatus  string    `json:"discord_status"`
	CalendarStatus string    `json:"calendar_status"`
	CreatedAt      time.Time `json:"created_at"`
}

// ID is deterministic (sha256 of threadID + event index + date, first 16
// hex chars) so re-processing the same email — a deliberate backfill
// rerun, or perception-svc's at-least-once redelivery — overwrites the
// same file instead of creating a duplicate pending reminder.
func ID(threadID string, index int, date string) string {
	h := sha256.Sum256(fmt.Appendf(nil, "%s|%d|%s", threadID, index, date))
	return hex.EncodeToString(h[:])[:16]
}

func dir(root string) string {
	return filepath.Join(root, "logs", "school-events")
}

func path(root, id string) string {
	return filepath.Join(dir(root), id+".json")
}

// Save writes e to disk, creating $root/logs/school-events/ if needed. If
// an event with the same ID already exists and both its channels are
// resolved (not "pending"), Save is a no-op — an already-fully-sent event
// is never resurrected by a re-processed email.
func Save(root string, e Event) error {
	if existing, err := read(root, e.ID); err == nil {
		if existing.DiscordStatus != "pending" && existing.CalendarStatus != "pending" {
			return nil
		}
		if existing.DiscordStatus != "pending" {
			e.DiscordStatus = existing.DiscordStatus
		}
		if existing.CalendarStatus != "pending" {
			e.CalendarStatus = existing.CalendarStatus
		}
	}

	if err := os.MkdirAll(dir(root), 0o755); err != nil {
		return fmt.Errorf("schoolevents: mkdir: %w", err)
	}
	return write(root, e)
}

func write(root string, e Event) error {
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("schoolevents: marshal %s: %w", e.ID, err)
	}
	if err := os.WriteFile(path(root, e.ID), b, 0o644); err != nil {
		return fmt.Errorf("schoolevents: write %s: %w", e.ID, err)
	}
	return nil
}

func read(root, id string) (Event, error) {
	b, err := os.ReadFile(path(root, id))
	if err != nil {
		return Event{}, err
	}
	var e Event
	if err := json.Unmarshal(b, &e); err != nil {
		return Event{}, fmt.Errorf("schoolevents: unmarshal %s: %w", id, err)
	}
	return e, nil
}

// MarkDiscordSent flips id's DiscordStatus to "sent".
func MarkDiscordSent(root, id string) error {
	e, err := read(root, id)
	if err != nil {
		return fmt.Errorf("schoolevents: read %s: %w", id, err)
	}
	e.DiscordStatus = "sent"
	return write(root, e)
}

// MarkCalendarStatus sets id's CalendarStatus to status ("sent" or "skipped").
func MarkCalendarStatus(root, id, status string) error {
	e, err := read(root, id)
	if err != nil {
		return fmt.Errorf("schoolevents: read %s: %w", id, err)
	}
	e.CalendarStatus = status
	return write(root, e)
}

// DueOrOverdue returns every event with at least one pending channel whose
// Date is on or before tomorrow (relative to now) and not more than
// staleCutoffDays past its own Date. A missing store directory (nothing
// has ever been queued) returns an empty slice, not an error.
func DueOrOverdue(root string, now time.Time) ([]Event, error) {
	entries, err := os.ReadDir(dir(root))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("schoolevents: read dir: %w", err)
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tomorrow := today.AddDate(0, 0, 1)
	cutoff := today.AddDate(0, 0, -staleCutoffDays)

	var due []Event
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		id := entry.Name()[:len(entry.Name())-len(filepath.Ext(entry.Name()))]
		e, readErr := read(root, id)
		if readErr != nil {
			continue
		}
		if e.DiscordStatus != "pending" && e.CalendarStatus != "pending" {
			continue
		}
		date, parseErr := time.ParseInLocation("2006-01-02", e.Date, now.Location())
		if parseErr != nil {
			continue
		}
		if date.After(tomorrow) {
			continue
		}
		if date.Before(cutoff) {
			continue
		}
		due = append(due, e)
	}
	return due, nil
}
