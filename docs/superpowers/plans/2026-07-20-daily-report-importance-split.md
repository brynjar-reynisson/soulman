# Daily Report Important/Not-Important Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split the daily report into two append-only files per day (important, not-important), combined into one `## Important` / `## Not Important` view wherever a report is read, with importance determined per source (error files and CLI notes always important; system-monitor critical/ok important, warning not; gmail triage's existing DeepSeek judgment reused).

**Architecture:** `action-svc/report.Entry` gains `Important bool`; `PathForDate` resolves to `daily-report-<date>.txt` (important, unchanged name) or `daily-report-<date>-fyi.txt` (not important); `Append` stays pure append-only per file; `Read` combines both files into one string. `web-svc/reports` mirrors this independently (existing deliberate duplication, no cross-module import). `thinking-svc/rules`' shared `errorReportParams` struct gains `Important bool`, set by each of the three mechanical rules that use it; `action-svc/dispatch` threads it from `ActionRequest.Parameters` into `report.Entry.Important`.

**Tech Stack:** Go — `action-svc`, `thinking-svc`, `web-svc` (three independent modules).

## Global Constraints

- The important file's name is unchanged from today (`daily-report-<date>.txt`) — no migration needed, existing/historical files remain valid and are treated as "all important."
- Both files stay pure append-only — no read-modify-write-in-the-middle. Only `Read` (in both `action-svc/report` and `web-svc/reports`) does any combining, and only at read time.
- `action-svc/scheduler/daily.go` (the 10:00 AM Discord digest) needs **no code change** — it already just calls `report.Read` and forwards the string verbatim.
- `web/src/components/ReportsPanel.tsx` needs **no code change** — it already renders `report.content` verbatim in a `<pre>` block; the `## Important`/`## Not Important` lines render as plain-text section breaks.
- Importance rule for `system_monitor`: `severity == "critical" || severity == "ok"` → important; `severity == "warning"` → not important. (`sysmonitor` only ever publishes on a severity *change*, so a published `"ok"` is always a just-recovered transition, never a steady-state ping — see spec for the full reasoning.)
- Spec of record: `docs/superpowers/specs/2026-07-20-daily-report-importance-split-design.md`.

---

### Task 1: `action-svc/report` — two files, combined on read

**Files:**
- Modify: `action-svc/report/report.go`
- Modify: `action-svc/report/report_test.go`
- Modify: `action-svc/scheduler/daily_test.go` (one call site, same module — see Interfaces)

**Interfaces:**
- Produces: `report.Entry.Important bool`; `report.PathForDate(root string, date time.Time, important bool) string` (signature change from 2 args to 3 — breaks every call site in this module); `report.Read` unchanged signature (`(string, error)`) but new combined-output behavior. Task 2 and Task 3 (same module, `action-svc/dispatch`) depend on `Entry.Important` existing.

- [ ] **Step 1: Write the failing tests**

Add to `action-svc/report/report_test.go` (after the existing tests):

```go
func TestPathForDate_ImportantVsNotImportant(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 20, 0, 0, 0, 0, time.Local)

	important := report.PathForDate(root, day, true)
	notImportant := report.PathForDate(root, day, false)

	if filepath.Base(important) != "daily-report-2026-07-20.txt" {
		t.Errorf("important path = %q, want daily-report-2026-07-20.txt", filepath.Base(important))
	}
	if filepath.Base(notImportant) != "daily-report-2026-07-20-fyi.txt" {
		t.Errorf("not-important path = %q, want daily-report-2026-07-20-fyi.txt", filepath.Base(notImportant))
	}
}

func TestAppend_ImportantEntry_WritesToImportantFile(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 20, 8, 0, 0, 0, time.Local)

	path, err := report.Append(root, report.Entry{Summary: "s", OccurredAt: day, Important: true})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if path != report.PathForDate(root, day, true) {
		t.Errorf("path = %q, want the important-file path", path)
	}
}

func TestAppend_NotImportantEntry_WritesToFYIFile(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 20, 8, 0, 0, 0, time.Local)

	path, err := report.Append(root, report.Entry{Summary: "s", OccurredAt: day, Important: false})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if path != report.PathForDate(root, day, false) {
		t.Errorf("path = %q, want the not-important-file path", path)
	}
}

func TestRead_CombinesImportantAndNotImportant_WithHeadings(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 20, 8, 0, 0, 0, time.Local)

	if _, err := report.Append(root, report.Entry{Summary: "important thing", OccurredAt: day, Important: true}); err != nil {
		t.Fatalf("append important: %v", err)
	}
	if _, err := report.Append(root, report.Entry{Summary: "fyi thing", OccurredAt: day, Important: false}); err != nil {
		t.Fatalf("append fyi: %v", err)
	}

	content, err := report.Read(root, day)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if !strings.Contains(content, "## Important") {
		t.Errorf("content missing '## Important' heading:\n%s", content)
	}
	if !strings.Contains(content, "## Not Important") {
		t.Errorf("content missing '## Not Important' heading:\n%s", content)
	}
	if !strings.Contains(content, "important thing") {
		t.Errorf("content missing important entry:\n%s", content)
	}
	if !strings.Contains(content, "fyi thing") {
		t.Errorf("content missing fyi entry:\n%s", content)
	}
	importantIdx := strings.Index(content, "## Important")
	notImportantIdx := strings.Index(content, "## Not Important")
	if importantIdx < 0 || notImportantIdx < 0 || importantIdx > notImportantIdx {
		t.Errorf("expected '## Important' to appear before '## Not Important', got:\n%s", content)
	}
}

func TestRead_OnlyImportantEntries_OmitsNotImportantHeading(t *testing.T) {
	root := t.TempDir()
	day := time.Date(2026, 7, 20, 8, 0, 0, 0, time.Local)

	if _, err := report.Append(root, report.Entry{Summary: "important thing", OccurredAt: day, Important: true}); err != nil {
		t.Fatalf("append: %v", err)
	}

	content, err := report.Read(root, day)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if strings.Contains(content, "## Not Important") {
		t.Errorf("content should not have '## Not Important' heading when that file is empty/missing:\n%s", content)
	}
}
```

Update the two existing tests that assert on `report.PathForDate(root, day)` (2-arg): `TestAppend_SecondEntry_PrecededByExactlyOneBlankLine` (line ~78) and `TestPathForDate_NormalizesToLocal` (lines ~214-227) — add `true` as the 3rd argument to every call, and add `Important: true` to every `report.Entry{...}` literal in `TestAppend_CreatesReportsDirAndFile`, `TestAppend_UsesOccurredAtDate_NotToday`, `TestAppend_SecondEntry_PrecededByExactlyOneBlankLine` (both entries), `TestAppend_SecondEntry_PrecededByExactlyOneBlankLine_WhenRawContentEndsInNewline` (both entries), and `TestAppend_SecondEntry_PrecededByExactlyOneBlankLine_WhenRawContentEndsInThreeNewlines` (both entries) — these tests are about the blank-line/separator logic, which must keep exercising the same single file per test (the important file), not accidentally split across both files now that `Important` defaults to `false`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C action-svc test ./report/... -v`
Expected: compile errors (`PathForDate` called with 2 args where 3 are now required at the new call sites; `Important` field undefined until Step 3) and/or assertion failures once it compiles (existing blank-line tests would otherwise silently break if `Important` isn't added to their entries, since Go's zero value `false` would route them to the FYI file — verify this is exactly why those literals need the field, not guessed).

- [ ] **Step 3: Implement**

Replace `action-svc/report/report.go`'s `Entry` struct, `PathForDate`, `Append`, and `Read` (keep `separatorFor` and `formatEntry` unchanged):

```go
// Entry is one daily-report entry, matching the format from
// error-report-action-design.md: a header line (timestamp, source
// directory, one-line summary) optionally followed by verbatim raw content.
// Important determines which of the two per-day files this entry is
// appended to — see docs/superpowers/specs/2026-07-20-daily-report-importance-split-design.md.
type Entry struct {
	Summary    string
	RawContent string
	SourcePath string
	OccurredAt time.Time
	Important  bool
}

// PathForDate returns the report file path for the given date and
// importance, using the YYYY-MM-DD of the *occurred* date (not "today")
// per the error-report spec. The important filename is unchanged from
// before this feature (daily-report-<date>.txt) — no migration needed.
// Shared by the dispatch handler (writes) and the scheduler/Read (reads)
// so they can never disagree on the filename convention.
func PathForDate(root string, date time.Time, important bool) string {
	suffix := ""
	if !important {
		suffix = "-fyi"
	}
	filename := fmt.Sprintf("daily-report-%s%s.txt", date.Local().Format("2006-01-02"), suffix)
	return filepath.Join(root, "reports", filename)
}

// Append writes one entry to the important or not-important file for
// entry.OccurredAt's date (per entry.Important), creating $root/reports/
// (and the file) if either is missing. If the target file already has
// content, the new entry is preceded by exactly one blank line.
//
// Append is not safe for concurrent use: it performs a read-then-write
// sequence (peeking the file's trailing newlines via separatorFor, possibly
// truncating, then appending) with no internal locking. Safe today because
// the sole caller (dispatch's NATS message handler) processes messages one
// at a time in a single goroutine; a future concurrent caller must add its
// own synchronization. The two files (important/not-important) are
// entirely independent — an important-entry append never touches the FYI
// file or vice versa.
func Append(root string, e Entry) (string, error) {
	dir := filepath.Join(root, "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("report: mkdir %s: %w", dir, err)
	}

	path := PathForDate(root, e.OccurredAt, e.Important)

	sep, err := separatorFor(path)
	if err != nil {
		return "", err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("report: open %s: %w", path, err)
	}
	defer f.Close()

	text := sep + formatEntry(e)

	if _, err := f.WriteString(text); err != nil {
		return "", fmt.Errorf("report: write %s: %w", path, err)
	}
	return path, nil
}

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

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("report: read %s: %w", path, err)
	}
	return string(b), nil
}

func combine(important, notImportant string) string {
	important = strings.TrimSpace(important)
	notImportant = strings.TrimSpace(notImportant)

	var b strings.Builder
	if important != "" {
		b.WriteString("## Important\n\n")
		b.WriteString(important)
		b.WriteString("\n")
	}
	if notImportant != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## Not Important\n\n")
		b.WriteString(notImportant)
		b.WriteString("\n")
	}
	return b.String()
}
```

- [ ] **Step 4: Fix the sibling `scheduler` package's call site (same module)**

In `action-svc/scheduler/daily_test.go`, `TestRunOnce_WhitespaceOnlyReport_SkipsSend` calls `report.PathForDate(root, yesterday)` — update to `report.PathForDate(root, yesterday, true)` (the test simulates a whitespace-only important-file report; the FYI file stays absent, and `combine` trims the important side to empty too, so the "skip send" assertion still holds).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go -C action-svc test ./report/... ./scheduler/... -v`
Expected: PASS — every pre-existing test plus the 5 new ones.

- [ ] **Step 6: Run the whole module's build**

Run: `go -C action-svc build ./...`
Expected: succeeds — confirms no other package in this module still calls the old 2-arg `PathForDate`.

- [ ] **Step 7: Commit**

```bash
git add action-svc/report/report.go action-svc/report/report_test.go action-svc/scheduler/daily_test.go
git commit -m "action-svc/report: split into important/not-important files, combine on read"
```

---

### Task 2: `action-svc/dispatch/report_entry.go` — thread `Important` through

**Files:**
- Modify: `action-svc/dispatch/report_entry.go`
- Modify: `action-svc/dispatch/dispatch_test.go`

**Interfaces:**
- Consumes: `report.Entry.Important` (Task 1).
- Produces: `ReportEntryParams.Important bool` (JSON `important`) — Task 6 (`thinking-svc/rules`) is the producer of this JSON field on the wire; this task is the consumer.

- [ ] **Step 1: Write the failing test**

Add to `action-svc/dispatch/dispatch_test.go` (after the existing `TestAppendReportEntry_RealImplementation_WritesReportFile`):

```go
func TestAppendReportEntry_Important_WritesToImportantFile(t *testing.T) {
	root := t.TempDir()
	params, _ := json.Marshal(map[string]any{
		"summary":     "critical error",
		"raw_content": "trace",
		"source_path": `C:\errors\file.txt`,
		"occurred_at": "2026-07-20T14:32:00-06:00",
		"important":   true,
	})

	path, err := dispatch.AppendReportEntry(root, params)
	if err != nil {
		t.Fatalf("AppendReportEntry: %v", err)
	}
	if filepath.Base(path) != "daily-report-2026-07-20.txt" {
		t.Errorf("path = %q, want the important (unsuffixed) filename", filepath.Base(path))
	}
}

func TestAppendReportEntry_NotImportant_WritesToFYIFile(t *testing.T) {
	root := t.TempDir()
	params, _ := json.Marshal(map[string]any{
		"summary":     "routine note",
		"raw_content": "",
		"source_path": `C:\errors\file.txt`,
		"occurred_at": "2026-07-20T14:32:00-06:00",
		"important":   false,
	})

	path, err := dispatch.AppendReportEntry(root, params)
	if err != nil {
		t.Fatalf("AppendReportEntry: %v", err)
	}
	if filepath.Base(path) != "daily-report-2026-07-20-fyi.txt" {
		t.Errorf("path = %q, want the not-important (-fyi) filename", filepath.Base(path))
	}
}
```

Add `"path/filepath"` to this test file's imports if not already present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C action-svc test ./dispatch/... -run TestAppendReportEntry_Important -v`
Expected: FAIL — `ReportEntryParams` has no `important` key to unmarshal yet, so both entries currently land in the not-important (FYI) file regardless of the `"important"` value in the test JSON (Go's zero-value default), making `TestAppendReportEntry_Important_WritesToImportantFile` fail.

- [ ] **Step 3: Implement**

In `action-svc/dispatch/report_entry.go`, update `ReportEntryParams` and the entry construction:

```go
type ReportEntryParams struct {
	Summary    string `json:"summary"`
	RawContent string `json:"raw_content"`
	SourcePath string `json:"source_path"`
	OccurredAt string `json:"occurred_at"`
	Important  bool   `json:"important"`
}

var AppendReportEntry = func(root string, params json.RawMessage) (string, error) {
	var p ReportEntryParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("dispatch: unmarshal params: %w", err)
	}
	occurredAt, err := time.Parse(time.RFC3339, p.OccurredAt)
	if err != nil {
		return "", fmt.Errorf("dispatch: parse occurred_at %q: %w", p.OccurredAt, err)
	}
	entry := report.Entry{
		Summary:    p.Summary,
		RawContent: p.RawContent,
		SourcePath: p.SourcePath,
		OccurredAt: occurredAt.Local(),
		Important:  p.Important,
	}
	path, err := report.Append(root, entry)
	if err != nil {
		return "", fmt.Errorf("dispatch: append report entry: %w", err)
	}
	return path, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C action-svc test ./dispatch/... -v`
Expected: PASS — all pre-existing tests plus the 2 new ones.

- [ ] **Step 5: Commit**

```bash
git add action-svc/dispatch/report_entry.go action-svc/dispatch/dispatch_test.go
git commit -m "action-svc/dispatch: thread Important through append_daily_report_entry"
```

---

### Task 3: `action-svc/dispatch/gmail_triage.go` — thread `Important` into the report entry

**Files:**
- Modify: `action-svc/dispatch/gmail_triage.go`
- Modify: `action-svc/dispatch/gmail_triage_test.go`

**Interfaces:**
- Consumes: `report.Entry.Important` (Task 1). `GmailTriageParams.Important` already exists on the wire (thinking-svc already sets it) — this task is purely about `AppendGmailReportEntry` finally using it for file placement, not just the existing Discord-notify decision.

- [ ] **Step 1: Write the failing tests**

Add to `action-svc/dispatch/gmail_triage_test.go` (after `TestAppendGmailReportEntry_RealImplementation_WritesReportFile`):

```go
func TestAppendGmailReportEntry_Important_WritesToImportantFile(t *testing.T) {
	root := t.TempDir()
	params := gmailTriageParamsJSON(t, "boss@company.com", "Server down", "outage", true)

	path, err := dispatch.AppendGmailReportEntry(root, params)
	if err != nil {
		t.Fatalf("AppendGmailReportEntry: %v", err)
	}
	if !strings.HasSuffix(path, "daily-report-2026-07-18.txt") {
		t.Errorf("path = %q, want the important (unsuffixed) filename", path)
	}
}

func TestAppendGmailReportEntry_NotImportant_WritesToFYIFile(t *testing.T) {
	root := t.TempDir()
	params := gmailTriageParamsJSON(t, "newsletter@example.com", "Weekly digest", "routine", false)

	path, err := dispatch.AppendGmailReportEntry(root, params)
	if err != nil {
		t.Fatalf("AppendGmailReportEntry: %v", err)
	}
	if !strings.HasSuffix(path, "daily-report-2026-07-18-fyi.txt") {
		t.Errorf("path = %q, want the not-important (-fyi) filename", path)
	}
}
```

Add `"strings"` to this test file's imports if not already present. (`gmailTriageParamsJSON`'s fixed `occurred_at` is `2026-07-18T09:00:00-06:00`, hence the expected `2026-07-18` filename.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C action-svc test ./dispatch/... -run TestAppendGmailReportEntry_Important -v`
Expected: FAIL — `AppendGmailReportEntry` doesn't set `Important` on the `report.Entry` yet, so both land in the FYI file regardless of the params value.

- [ ] **Step 3: Implement**

In `action-svc/dispatch/gmail_triage.go`, add `Important: p.Important` to the `report.Entry{...}` literal inside `AppendGmailReportEntry`:

```go
	entry := report.Entry{
		Summary:    fmt.Sprintf("%s — deemed %s", p.Subject, verdict),
		RawContent: fmt.Sprintf("Reason: %s\n\n%s", p.Reason, p.BodyExcerpt),
		SourcePath: p.Sender + "/" + p.ThreadID,
		OccurredAt: occurredAt.Local(),
		Important:  p.Important,
	}
```

(No other lines in this function change.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C action-svc test ./dispatch/... -v`
Expected: PASS — all pre-existing tests plus the 2 new ones.

- [ ] **Step 5: Commit**

```bash
git add action-svc/dispatch/gmail_triage.go action-svc/dispatch/gmail_triage_test.go
git commit -m "action-svc/dispatch: gmail triage's existing importance now also drives file placement"
```

---

### Task 4: `web-svc/reports` — mirror the split (independent module)

**Files:**
- Modify: `web-svc/reports/reports.go`
- Modify: `web-svc/reports/reports_test.go`

**Interfaces:** None new — `PathForDate`/`Read`'s public signatures used by `web-svc/httpserver/reports_handler.go` stay compatible (`Read(root, date) (content string, found bool, err error)` is unchanged in shape; only its internal behavior and `PathForDate`'s arg count change, and `PathForDate` isn't called from `reports_handler.go` at all — only `Read` is, so no changes are needed there).

- [ ] **Step 1: Write the failing tests**

Replace `web-svc/reports/reports_test.go` entirely:

```go
package reports_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"soulman/web-svc/reports"
)

func TestPathForDate_ImportantVsNotImportant(t *testing.T) {
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.Local)

	important := reports.PathForDate(`C:\root`, date, true)
	notImportant := reports.PathForDate(`C:\root`, date, false)

	wantImportant := filepath.Join(`C:\root`, "reports", "daily-report-2026-07-19.txt")
	wantNotImportant := filepath.Join(`C:\root`, "reports", "daily-report-2026-07-19-fyi.txt")
	if important != wantImportant {
		t.Errorf("important path = %q, want %q", important, wantImportant)
	}
	if notImportant != wantNotImportant {
		t.Errorf("not-important path = %q, want %q", notImportant, wantNotImportant)
	}
}

func TestRead_ImportantFileOnly_ReturnsWrappedContentAndFound(t *testing.T) {
	root := t.TempDir()
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.Local)
	dir := filepath.Join(root, "reports")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "daily-report-2026-07-19.txt"), []byte("2026-07-19 09:00  [test]  hello"), 0o644)

	content, found, err := reports.Read(root, date)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if content != "## Important\n\n2026-07-19 09:00  [test]  hello\n" {
		t.Errorf("content = %q", content)
	}
}

func TestRead_BothFiles_CombinesWithHeadings(t *testing.T) {
	root := t.TempDir()
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.Local)
	dir := filepath.Join(root, "reports")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "daily-report-2026-07-19.txt"), []byte("important entry"), 0o644)
	os.WriteFile(filepath.Join(dir, "daily-report-2026-07-19-fyi.txt"), []byte("fyi entry"), 0o644)

	content, found, err := reports.Read(root, date)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	want := "## Important\n\nimportant entry\n\n## Not Important\n\nfyi entry\n"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}

func TestRead_MissingFile_ReturnsNotFoundNoError(t *testing.T) {
	root := t.TempDir()
	date := time.Date(2026, 7, 19, 0, 0, 0, 0, time.Local)

	content, found, err := reports.Read(root, date)
	if err != nil {
		t.Fatalf("Read() error = %v, want nil", err)
	}
	if found {
		t.Fatal("found = true, want false")
	}
	if content != "" {
		t.Errorf("content = %q, want empty", content)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C web-svc test ./reports/... -v`
Expected: compile errors (`PathForDate` called with 2 args; new expectations don't match old behavior).

- [ ] **Step 3: Implement**

Replace `web-svc/reports/reports.go` entirely:

```go
// Package reports reads action-svc's daily report files
// ($SOULMAN_ROOT/reports/daily-report-YYYY-MM-DD.txt for important entries,
// daily-report-YYYY-MM-DD-fyi.txt for not-important ones). This duplicates
// action-svc/report's PathForDate/Read logic rather than adding a
// cross-module Go dependency, consistent with this codebase's convention
// of keeping each service an independent module.
package reports

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PathForDate returns the report file path for the given date and
// importance. The important filename is unchanged from before this
// feature (daily-report-<date>.txt).
func PathForDate(root string, date time.Time, important bool) string {
	suffix := ""
	if !important {
		suffix = "-fyi"
	}
	filename := fmt.Sprintf("daily-report-%s%s.txt", date.Format("2006-01-02"), suffix)
	return filepath.Join(root, "reports", filename)
}

// Read returns the combined view for the given date: important entries
// under a "## Important" heading, not-important entries under a
// "## Not Important" heading. found is true if either file exists; false
// (with a nil error) only if neither does — that's an expected,
// non-error condition, not a failure.
func Read(root string, date time.Time) (content string, found bool, err error) {
	important, importantFound, err := readOne(PathForDate(root, date, true))
	if err != nil {
		return "", false, err
	}
	notImportant, notImportantFound, err := readOne(PathForDate(root, date, false))
	if err != nil {
		return "", false, err
	}
	return combine(important, notImportant), importantFound || notImportantFound, nil
}

func readOne(path string) (content string, found bool, err error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reports: read %s: %w", path, err)
	}
	return string(b), true, nil
}

func combine(important, notImportant string) string {
	important = strings.TrimSpace(important)
	notImportant = strings.TrimSpace(notImportant)

	var b strings.Builder
	if important != "" {
		b.WriteString("## Important\n\n")
		b.WriteString(important)
		b.WriteString("\n")
	}
	if notImportant != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## Not Important\n\n")
		b.WriteString(notImportant)
		b.WriteString("\n")
	}
	return b.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C web-svc test ./reports/... -v`
Expected: PASS — all 4 tests.

- [ ] **Step 5: Run the whole module's build**

Run: `go -C web-svc build ./...`
Expected: succeeds.

- [ ] **Step 6: Commit**

```bash
git add web-svc/reports/reports.go web-svc/reports/reports_test.go
git commit -m "web-svc/reports: mirror the important/not-important file split"
```

---

### Task 5: `thinking-svc/rules` — set `Important` per source

**Files:**
- Modify: `thinking-svc/rules/error_report.go`
- Modify: `thinking-svc/rules/cli_note.go`
- Modify: `thinking-svc/rules/system_monitor.go`
- Modify: `thinking-svc/rules/error_report_test.go`
- Modify: `thinking-svc/rules/cli_note_test.go`
- Modify: `thinking-svc/rules/system_monitor_test.go`

**Interfaces:**
- Produces: `important` key in the JSON `Parameters` these three rules emit — consumed by `action-svc/dispatch`'s `ReportEntryParams.Important` (Task 2, already built to read this key; no coordination needed since Task 2 already unmarshals whatever `important` value is present, defaulting `false` if absent).

- [ ] **Step 1: Write the failing tests**

In `thinking-svc/rules/error_report_test.go`, add an assertion to the existing `TestErrorReportRule_Handle_BuildsActionRequest` (after the `source_path` check):

```go
	if params["important"] != true {
		t.Errorf("important = %v, want true", params["important"])
	}
```

In `thinking-svc/rules/cli_note_test.go`, extend the `params` struct and add an assertion in `TestCLINoteRule_Handle_BuildsActionRequest`:

```go
	var params struct {
		Summary    string     `json:"summary"`
		RawContent string     `json:"raw_content"`
		SourcePath string     `json:"source_path"`
		OccurredAt *time.Time `json:"occurred_at"`
		Important  bool       `json:"important"`
	}
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	if params.Summary != "disk cleanup done" {
		t.Errorf("Summary = %q, want verbatim text", params.Summary)
	}
	if params.RawContent != "disk cleanup done" {
		t.Errorf("RawContent = %q, want verbatim text", params.RawContent)
	}
	if params.SourcePath != "cli/note" {
		t.Errorf(`SourcePath = %q, want "cli/note"`, params.SourcePath)
	}
	if !params.Important {
		t.Error("Important = false, want true for a CLI note")
	}
```

In `thinking-svc/rules/system_monitor_test.go`, add a new table-driven test (after the existing tests, before `TestMatch_FindsSystemMonitorRule`):

```go
func TestSystemMonitorRule_Handle_Important(t *testing.T) {
	cases := []struct {
		name     string
		severity string
		want     bool
	}{
		{"critical is important", "critical", true},
		{"ok is important (edge-triggered publish means ok is always a recovery)", "ok", true},
		{"warning is not important", "warning", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			specific, _ := json.Marshal(struct {
				CheckType string `json:"check_type"`
				Path      string `json:"path,omitempty"`
				Severity  string `json:"severity"`
			}{CheckType: "disk_space", Path: `C:\`, Severity: tc.severity})

			occurred := time.Now()
			s := &common.Stimulus{
				StimulusID: "stim-sysmon-importance",
				Channel:    "system-monitor",
				ReceivedAt: time.Now().UTC(),
				OccurredAt: &occurred,
				Content: common.Content{
					RawText:     "message",
					ContentType: "text",
					RawPayload:  json.RawMessage(`{}`),
				},
				ChannelMeta: common.ChannelMeta{ChannelSpecific: specific},
				Hints:       common.Hints{Priority: "normal", Tags: []string{"system", "system-monitor", "disk_space"}},
				Override:    common.Override{Params: json.RawMessage(`{}`)},
			}

			req, err := rules.SystemMonitorRule.Handle(context.Background(), s, &fakeSummarizer{})
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}

			var params struct {
				Important bool `json:"important"`
			}
			if err := json.Unmarshal(req.Parameters, &params); err != nil {
				t.Fatalf("decode Parameters: %v", err)
			}
			if params.Important != tc.want {
				t.Errorf("severity=%q: important = %v, want %v", tc.severity, params.Important, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C thinking-svc test ./rules/... -run 'TestErrorReportRule_Handle_BuildsActionRequest|TestCLINoteRule_Handle_BuildsActionRequest|TestSystemMonitorRule_Handle_Important' -v`
Expected: FAIL — `important` isn't in any of the three rules' marshaled params yet (`error_report`/`cli_note` fail the new assertions; `system_monitor`'s test fails to compile until `Important` exists on `errorReportParams`, or fails all three subtests once it compiles since importance isn't computed yet).

- [ ] **Step 3: Implement**

In `thinking-svc/rules/error_report.go`, add `Important bool` to the shared `errorReportParams` struct (this is the single struct all three rules marshal into):

```go
type errorReportParams struct {
	Summary    string     `json:"summary"`
	RawContent string     `json:"raw_content"`
	SourcePath string     `json:"source_path"`
	OccurredAt *time.Time `json:"occurred_at"`
	Important  bool       `json:"important"`
}
```

In the same file, `handleErrorReport`'s `errorReportParams{...}` literal gains `Important: true`:

```go
	params, err := json.Marshal(errorReportParams{
		Summary:    summary,
		RawContent: s.Content.RawText,
		SourcePath: sourcePath,
		OccurredAt: occurredAtValue(s),
		Important:  true,
	})
```

In `thinking-svc/rules/cli_note.go`, `handleCLINote`'s literal gains `Important: true`:

```go
	params, err := json.Marshal(errorReportParams{
		Summary:    s.Content.RawText,
		RawContent: s.Content.RawText,
		SourcePath: "cli/note",
		OccurredAt: s.OccurredAt,
		Important:  true,
	})
```

In `thinking-svc/rules/system_monitor.go`, `handleSystemMonitor`'s literal gains `Important: systemMonitorImportant(s)`, and a new helper is added:

```go
func handleSystemMonitor(_ context.Context, s *common.Stimulus, _ llm.Client) (*common.ActionRequest, error) {
	params, err := json.Marshal(errorReportParams{
		Summary:    s.Content.RawText,
		RawContent: s.Content.RawText,
		SourcePath: systemMonitorSourcePath(s),
		OccurredAt: s.OccurredAt,
		Important:  systemMonitorImportant(s),
	})
	if err != nil {
		return nil, fmt.Errorf("rules: marshal system monitor parameters: %w", err)
	}

	req := &common.ActionRequest{
		CorrelationID:   uuid.NewString(),
		Intent:          "Log this system monitor alert to today's daily report",
		ActionHint:      "append_daily_report_entry",
		Parameters:      params,
		RiskLevel:       "low",
		Urgency:         "normal",
		ExpectedOutcome: "one entry appended to today's report file",
		Fallback:        "if fs-agent fails, retry once; if it fails again, log to episodic memory with error:execution tag and give up silently — a missed report entry is not worth interrupting the human",
	}
	return req, nil
}

// systemMonitorImportant is true for a critical severity (the system is
// down, or a resource hit its critical threshold) and for an ok severity —
// perception-svc's sysmonitor only ever publishes on a severity *change*
// (edge-triggered), so a published "ok" is never a steady-state "still
// fine" ping; it is always a just-recovered transition, symmetric with
// critical. Only "warning" (a resource crossing its softer threshold, not
// yet critical) stays not-important.
func systemMonitorImportant(s *common.Stimulus) bool {
	var meta struct {
		Severity string `json:"severity"`
	}
	if len(s.ChannelMeta.ChannelSpecific) > 0 {
		_ = json.Unmarshal(s.ChannelMeta.ChannelSpecific, &meta)
	}
	return meta.Severity == "critical" || meta.Severity == "ok"
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go -C thinking-svc test ./rules/... -v`
Expected: PASS — every pre-existing test in the `rules` package plus the new/updated assertions.

- [ ] **Step 5: Commit**

```bash
git add thinking-svc/rules/error_report.go thinking-svc/rules/cli_note.go thinking-svc/rules/system_monitor.go thinking-svc/rules/error_report_test.go thinking-svc/rules/cli_note_test.go thinking-svc/rules/system_monitor_test.go
git commit -m "thinking-svc/rules: set Important per source (error/cli always, system-monitor by severity)"
```

---

### Task 6: Update `action-svc/NOTES.md`, `thinking-svc/NOTES.md`, `web-svc/NOTES.md`, and root `CLAUDE.md`

**Files:**
- Modify: `action-svc/NOTES.md`
- Modify: `thinking-svc/NOTES.md`
- Modify: `web-svc/NOTES.md`
- Modify: `CLAUDE.md`

**Interfaces:** None (documentation only).

- [ ] **Step 1: Add a section to `action-svc/NOTES.md`**

After the existing `## Feign mode` section (end of file), add:

```markdown

## Important/not-important report split (added 2026-07-20)

See `docs/superpowers/specs/2026-07-20-daily-report-importance-split-design.md`. Each day now has two independent append-only report files: `daily-report-<date>.txt` (important — unchanged name from before this feature, so historical files need no migration) and `daily-report-<date>-fyi.txt` (not important). `report.Read` (used by both the 10:00 AM Discord digest and, independently, `web-svc/reports`) combines the two into one `## Important` / `## Not Important` string at read time — the write path (`report.Append`) never does any read-modify-write across the two files, only simple appends within whichever one file a given entry targets.

`web-svc/reports` is a separate module that already deliberately duplicates this package's `PathForDate`/`Read` logic (see that package's own doc comment) rather than cross-importing — its copy was updated in lockstep by hand, the same way the two copies have always been kept in sync.
```

- [ ] **Step 2: Add a section to `thinking-svc/NOTES.md`**

After the existing `## Publisher: now JetStream-backed` section (end of file), add:

```markdown

## System Monitor importance: `ok` is always a recovery (added 2026-07-20)

`systemMonitorImportant` (`thinking-svc/rules/system_monitor.go`) treats `severity == "ok"` as important, same as `critical` — this isn't a guess, it follows directly from `perception-svc/sysmonitor`'s edge-triggered publish design: a `Stimulus` is only ever published when severity *changes*, so a published `"ok"` can never represent "still fine" (that state is never published at all) — it always means "just recovered from warning or critical." If `sysmonitor`'s publish semantics ever changed to also publish steady-state pings, this reasoning would break and `systemMonitorImportant` would need revisiting.
```

- [ ] **Step 3: Add a section to `web-svc/NOTES.md`**

After the existing `## auth.Verifier's JWKS cache never refreshes or refetches on an unknown kid` section (end of file), add:

```markdown

## `reports.Read` now combines two files, not one (added 2026-07-20)

See `docs/superpowers/specs/2026-07-20-daily-report-importance-split-design.md`. `reports.PathForDate` gained an `important bool` parameter and `reports.Read`'s return value changed from one file's raw bytes to a combined `## Important` / `## Not Important` string — `web-svc/httpserver/reports_handler.go` needed no changes at all, since it only calls `reports.Read` and forwards `content` through unchanged.
```

- [ ] **Step 4: Update `CLAUDE.md`**

Find the `action-svc` bullet's Specs line (ends with `2026-07-19-action-svc-feign-mode-design.md`) and append:

```
- Specs: `2026-07-17-action-svc-design.md`, `2026-07-17-daily-report-delivery-design.md`, `2026-07-17-error-report-action-design.md`, `2026-07-18-gmail-triage-action-design.md`, `2026-07-18-pipeline-debugging-tools-design.md`, `2026-07-19-action-svc-feign-mode-design.md`, `2026-07-20-daily-report-importance-split-design.md`
```

Find the `thinking-svc` bullet's Specs line (ends with `2026-07-18-system-monitor-channel-design.md`) and append:

```
- Specs: `2026-07-17-thinking-svc-design.md`, `2026-07-18-gmail-triage-action-design.md`, `2026-07-18-system-monitor-channel-design.md`, `2026-07-20-daily-report-importance-split-design.md`
```

Find the `web-svc` bullet's Specs line (ends with `2026-07-20-dashboard-status-merge-and-raw-input-modal-design.md`) and append:

```
- Specs: `2026-07-19-soulman-web-dashboard-design.md`, `2026-07-20-system-monitor-dashboard-panel-design.md`, `2026-07-20-dashboard-status-merge-and-raw-input-modal-design.md`, `2026-07-20-daily-report-importance-split-design.md`
```

- [ ] **Step 5: Commit**

```bash
git add action-svc/NOTES.md thinking-svc/NOTES.md web-svc/NOTES.md CLAUDE.md
git commit -m "docs: note the daily report important/not-important split"
```

---

## Final Verification

After all 6 tasks:

- [ ] `go -C action-svc test ./... && go -C thinking-svc test ./... && go -C web-svc test ./...` — expect all PASS.
- [ ] `go -C action-svc build ./... && go -C thinking-svc build ./... && go -C web-svc build ./... && go -C perception-svc build ./... && go -C memory-svc build ./...` — expect all five services still build.
- [ ] Manually trigger one of each entry type (an error file, a CLI note, a system-monitor transition, a gmail triage of each importance) against dev and confirm: important entries land in `daily-report-<date>.txt`, not-important entries land in `daily-report-<date>-fyi.txt`, and the web dashboard's Daily Report panel shows both under `## Important`/`## Not Important` headings in one view.
