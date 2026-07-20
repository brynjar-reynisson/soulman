# Daily Report: Important vs Not Important Split

**Date:** 2026-07-20
**Status:** Approved
**Phase:** Soulman Phase 2 — extends `docs/superpowers/specs/2026-07-17-error-report-action-design.md`'s report-file mechanism and `docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md`'s existing importance judgment, across three services (`thinking-svc`, `action-svc`, `web-svc`).

---

## Summary

The daily report file currently interleaves every entry chronologically with no distinction between "an error file landed" and "a newsletter arrived, judged unimportant." This adds an `Important bool` to every report entry and splits storage into two parallel append-only files per day — `daily-report-<date>.txt` (important, same filename as today, backward compatible) and `daily-report-<date>-fyi.txt` (not important) — combined into one `## Important` / `## Not Important` view wherever a report is read (the 10:00 AM Discord digest, the web dashboard). Writes stay pure append-only in both files; only the read path does any combining.

---

## Importance rules

| Source | Importance |
|---|---|
| `error_report.go` (folder-watcher / DigitalMe error files) | Always important |
| `cli_note.go` (`soulman note "..."`) | Always important |
| `system_monitor.go` (disk/memory/cpu/service_health) | `severity == "critical"` **or** `severity == "ok"` → important; `severity == "warning"` → not important |
| `gmail_triage.go` | Already computed via DeepSeek (existing `Important` field) — now also drives file placement, not just the Discord notify decision |

The `system_monitor` rule deserves explanation: `perception-svc/sysmonitor`'s `Watcher` only ever publishes a `Stimulus` on a severity **change** (edge-triggered — see `docs/superpowers/specs/2026-07-18-system-monitor-channel-design.md`). A published `severity == "ok"` is therefore never a steady-state "still fine" ping; it is always a just-recovered transition. That makes `critical` (went down, or a resource hit its critical threshold) and `ok` (came back up) symmetric "important" moments — matching "system down/up again times are important" — while `warning` (a resource crossing its softer threshold, not yet critical) stays informational-only.

---

## `action-svc/report` — two files, combined on read

```go
type Entry struct {
	Summary    string
	RawContent string
	SourcePath string
	OccurredAt time.Time
	Important  bool
}

// PathForDate picks the important or "-fyi" file for the given date.
// The important filename is unchanged from today (daily-report-<date>.txt)
// — no migration needed for existing files or the Discord digest's naming
// assumptions.
func PathForDate(root string, date time.Time, important bool) string {
	suffix := ""
	if !important {
		suffix = "-fyi"
	}
	filename := fmt.Sprintf("daily-report-%s%s.txt", date.Local().Format("2006-01-02"), suffix)
	return filepath.Join(root, "reports", filename)
}
```

`Append` is unchanged in every way except it now resolves its path via `PathForDate(root, e.OccurredAt, e.Important)` — the existing `separatorFor`/blank-line/truncate-trailing-newlines logic is untouched and applies independently to whichever of the two files this entry lands in. Both files remain exactly as safe (and exactly as unsafe for concurrent use, for the same already-documented reason: sequential single-goroutine dispatch) as the single file is today.

`Read` changes from "return one file's bytes" to "combine both files":

```go
// Read returns the combined view for the given date: important entries
// under a "## Important" heading, not-important entries under a
// "## Not Important" heading. A missing/empty file contributes no section
// at all (not an empty header) — if neither file has content, returns "".
func Read(root string, date time.Time) (string, error) {
	important, err := readFile(PathForDate(root, date, true))
	if err != nil {
		return "", err
	}
	notImportant, err := readFile(PathForDate(root, date, false))
	if err != nil {
		return "", err
	}
	return combine(important, notImportant), nil
}
```

`combine` trims each side, writes `## Important\n\n<content>\n` if non-empty, then (with a blank line separating the two sections when both are present) `## Not Important\n\n<content>\n` if non-empty. This is what `action-svc/scheduler/daily.go`'s 10:00 AM Discord digest sends verbatim (that file needs **no code change at all** — it already just calls `report.Read` and forwards the string) and what `web-svc`'s `/api/reports/*` endpoints return as `content`.

---

## `web-svc/reports` — mirrored, independently

This package already deliberately duplicates `action-svc/report`'s `PathForDate`/`Read` rather than cross-importing (per its own existing doc comment, keeping every service an independent module). It gets the identical `PathForDate(root, date, important bool)` and combine-on-`Read` treatment, kept in sync by hand the same way the two copies already are today.

---

## `action-svc/dispatch` — thread `Important` through

- `report_entry.go`'s `ReportEntryParams` gains `Important bool \`json:"important"\`` (defaults to Go's zero value, `false`, if a caller's JSON omits it — every real caller after this change always sets it explicitly, so this only matters for malformed/legacy messages, which is an acceptable soft-fail: they land in the FYI file rather than crashing).
- `gmail_triage.go`'s `AppendGmailReportEntry` already unmarshals `p.Important` (used today only for the Discord-notify decision) — now also passed into `report.Entry.Important`.

---

## `thinking-svc/rules` — set `Important` per source

`errorReportParams` (the struct shared by `error_report.go`/`cli_note.go`/`system_monitor.go`) gains `Important bool \`json:"important"\``:

```go
type errorReportParams struct {
	Summary    string     `json:"summary"`
	RawContent string     `json:"raw_content"`
	SourcePath string     `json:"source_path"`
	OccurredAt *time.Time `json:"occurred_at"`
	Important  bool       `json:"important"`
}
```

- `handleErrorReport`: `Important: true`.
- `handleCLINote`: `Important: true`.
- `handleSystemMonitor`: `Important: systemMonitorImportant(s)`, a new helper reading `severity` out of `channel_metadata.channel_specific` (the same JSON blob `systemMonitorSourcePath` already parses, extended with one more field) and returning `severity == "critical" || severity == "ok"`.

`gmail_triage.go`'s rule is unchanged — it already computes and sets `Important` in its own distinct params struct (`gmailTriageParams`, not `errorReportParams`).

---

## Dashboard (`web/`)

`ReportsPanel.tsx` already renders `report.content` verbatim inside a `<pre>` block — the `## Important`/`## Not Important` lines show up as plain-text section breaks with zero component changes required. Deliberately not adding heading styling in this iteration (YAGNI — easy follow-up once the plain-text version has been seen in practice).

---

## Error Handling

| Failure | Behaviour |
|---|---|
| Only one of the two files exists for a date | `Read`/`combine` shows only that one section — no empty "## Not Important" header for a day with nothing unimportant |
| Neither file exists | `Read` returns `""`, same as today's "no report" behavior — `scheduler.RunOnce`'s existing `strings.TrimSpace(content) == ""` skip-send check keeps working unmodified |
| A malformed/legacy `append_daily_report_entry` message with no `important` key | Unmarshals to `false` (Go zero value) — lands in the FYI file rather than erroring; acceptable soft-fail, not a real scenario post-migration since every thinking-svc rule always sets it |

---

## Testing

- `action-svc/report/report_test.go`: `PathForDate` returns the two distinct filenames for `important=true/false`; `Append` writes important/not-important entries to their respective files with existing separator behavior intact per file; `Read` combines both correctly (both present, only one present, neither present); existing single-file tests updated for the new `PathForDate`/`Read` signatures.
- `action-svc/dispatch/report_entry_test.go` / `gmail_triage_test.go`: `Important` round-trips from `ReportEntryParams`/`GmailTriageParams` into `report.Entry.Important`, verified by checking which file the entry landed in.
- `action-svc/scheduler/daily_test.go`: updated for `report.Read`'s new combined-format output (no scheduler logic change, just fixture updates).
- `web-svc/reports/reports_test.go`: mirrors the `action-svc/report` test updates for its independent copy.
- `thinking-svc/rules/error_report_test.go` / `cli_note_test.go`: assert `Important: true` in the marshaled params.
- `thinking-svc/rules/system_monitor_test.go`: table-driven case per severity (`critical`→true, `ok`→true, `warning`→false).

---

## Out of Scope (this iteration)

- Any UI treatment beyond what `ReportsPanel`'s existing `<pre>` block already gives for free (no real headings, no collapsible sections, no filtering toggle).
- Migrating/backfilling historical single-file reports into the new two-file scheme — old `daily-report-<date>.txt` files from before this change simply become "all important" retroactively (their content lands in the file that's still named the same way), which is a reasonable, harmless default.
- Any change to the DeepSeek Gmail classifier itself — this reuses its existing importance judgment unmodified.

---

## Related

- `docs/superpowers/specs/2026-07-17-error-report-action-design.md` — original `append_daily_report_entry` action and report-file format this extends.
- `docs/superpowers/specs/2026-07-17-daily-report-delivery-design.md` — the 10:00 AM Discord digest cron, unmodified by this change (consumes `report.Read`'s new combined output transparently).
- `docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md` — source of the existing `Important` judgment this reuses for file placement.
- `docs/superpowers/specs/2026-07-18-system-monitor-channel-design.md`, `docs/superpowers/specs/2026-07-19-system-monitor-service-health-design.md` — the edge-triggered publish semantics `systemMonitorImportant`'s reasoning depends on.
