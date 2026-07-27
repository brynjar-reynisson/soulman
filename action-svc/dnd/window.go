// Package dnd implements action-svc's do-not-disturb window: a configurable
// time-of-day range during which real-time Discord notifications are
// accumulated to a pending file instead of sent immediately, flushed as one
// message once the window ends. Mirrors action-svc/feign's Gate-wrapping-
// Notifier shape and reuses action-svc/scheduler's wake-loop mechanics. See
// docs/superpowers/specs/2026-07-27-discord-do-not-disturb-design.md.
package dnd

import (
	"strconv"
	"strings"
	"time"
)

// Window is a do-not-disturb time-of-day range, local time, "HH:MM"
// boundaries.
type Window struct {
	Start string // "HH:MM", local time
	End   string // "HH:MM", local time
}

// Active reports whether t's local wall-clock time falls inside
// [Start, End). Handles both the non-wrapping case (Start < End, e.g.
// 00:00-10:00) and the midnight-wrapping case (Start > End, e.g.
// 22:00-06:00) — the window is configurable, and wrapping is a realistic
// real-world DND configuration even though it's not today's default. A
// zero-width window (Start == End) is never active — there's no duration
// to suppress.
func (w Window) Active(t time.Time) bool {
	startH, startM := parseTime(w.Start)
	endH, endM := parseTime(w.End)

	startMinutes := startH*60 + startM
	endMinutes := endH*60 + endM
	nowMinutes := t.Hour()*60 + t.Minute()

	if startMinutes == endMinutes {
		return false
	}
	if startMinutes < endMinutes {
		return nowMinutes >= startMinutes && nowMinutes < endMinutes
	}
	return nowMinutes >= startMinutes || nowMinutes < endMinutes
}

// parseTime parses "HH:MM" the same way scheduler.parseSendTime does:
// silent fallback to 10:00 on any malformed input, not fatal — matches
// action-svc/config's loose treatment of do_not_disturb.start/.end (no
// validation at the config-loading layer; this is where the fallback
// actually happens).
func parseTime(s string) (int, int) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 10, 0
	}
	hh, err1 := strconv.Atoi(parts[0])
	mm, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 10, 0
	}
	return hh, mm
}
