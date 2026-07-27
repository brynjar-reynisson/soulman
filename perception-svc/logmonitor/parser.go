// Package logmonitor implements perception-svc's Log Error pull channel:
// tails every sibling service's *-startup-err.log file for new slog
// ERROR-level lines and publishes a Stimulus the first time a given
// (service, message) pair is seen. See
// docs/superpowers/specs/2026-07-27-log-error-perception-design.md.
package logmonitor

import "regexp"

// lineRe matches one line in log/slog's default (unconfigured) handler
// format: "<date> <time> <LEVEL> <msg> [key=value ...]" — the classic Go
// log package timestamp (two space-separated tokens: date, then time)
// followed by the level, then everything else. This is what every Soulman
// service produces by calling the slog package-level functions directly
// against the default logger, per the 2026-07-27 logging conversion (see
// root CLAUDE.md's "Logging" section) — none of the five services install
// a custom handler or call slog.SetDefault.
var lineRe = regexp.MustCompile(`^\S+\s+\S+\s+(ERROR|WARN|INFO|DEBUG)\s+(.*)$`)

// attrStartRe finds the first slog key=value attribute boundary within the
// "msg [key=value ...]" remainder — a space followed by a bare identifier
// (letters/digits/underscore, no spaces) followed directly by "=". Every
// attribute key in this codebase is a simple snake_case identifier (see the
// 2026-07-27 logging conversion's call sites), and no message text in this
// codebase contains a literal "identifier=" substring, so the first match
// reliably marks where msg ends and attrs begin.
var attrStartRe = regexp.MustCompile(`\s[A-Za-z_][A-Za-z0-9_]*=`)

// attrAtStartRe matches an attribute at the very start of a string
// (no leading space). Used to detect when there is no message text
// before the first attribute, indicating an empty message.
var attrAtStartRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// ParsedLine is one successfully parsed ERROR-level log line.
type ParsedLine struct {
	Level string
	Msg   string
}

// parseLine attempts to parse line as one slog default-handler-formatted
// log line. Returns ok=false for any line that doesn't match this shape
// (stack-trace continuations, panics, or any other non-slog output) or
// whose level isn't ERROR — logmonitor only tracks Error-level lines, per
// the design spec's explicit out-of-scope decision on WARN monitoring.
func parseLine(line string) (ParsedLine, bool) {
	m := lineRe.FindStringSubmatch(line)
	if m == nil {
		return ParsedLine{}, false
	}
	level := m[1]
	if level != "ERROR" {
		return ParsedLine{}, false
	}
	rest := m[2]
	msg := rest

	// If rest starts immediately with an attribute (no message text),
	// msg is empty. This prevents an empty message from absorbing
	// the attributes into the Msg field, which would break dedup.
	if attrAtStartRe.MatchString(rest) {
		msg = ""
	} else if loc := attrStartRe.FindStringIndex(rest); loc != nil {
		msg = rest[:loc[0]]
	}

	return ParsedLine{Level: level, Msg: msg}, true
}
