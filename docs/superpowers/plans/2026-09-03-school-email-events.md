# School Email Events Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract actionable dates/times from `@reykjavik.is` school emails and remind both parents the day before — a Discord message to the owner, a Google Calendar invite to the other parent — running prod-only, with a one-off backfill for mail since August 2026.

**Architecture:** New `thinking-svc` rule (`SchoolEmailRule`, ahead of the generic Gmail rule) extracts events via a new DeepSeek prompt; a new `action-svc` dispatch handler persists future-dated events to a per-event JSON store with independent Discord/Calendar status; a new daily scheduler (with startup catch-up) fires reminders at a configured time. A new `cli` subcommand backfills historical mail through the existing debug-injection endpoint.

**Tech Stack:** Go 1.25, `golang.org/x/oauth2`, `google.golang.org/api/calendar/v3`, `google.golang.org/api/gmail/v1` (cli only), existing NATS/JetStream pipeline.

**Spec:** `docs/superpowers/specs/2026-09-03-school-email-events-design.md`

## Global Constraints

- Sender domain allowlist for v1: `@reykjavik.is` (case-insensitive suffix match on the sender address).
- `school.enabled` is `true` in `config/prod.json`, `false` in `config/dev.json` — this is the entire prod-only gating mechanism; both env config files also get `school.sender_domains` set, but only prod's `enabled: true` actually activates anything.
- `school.notify_time` default `"16:00"` (local time, same `"HH:MM"` convention as `report_send_time`/`do_not_disturb`).
- `school.calendar_recipient_emails` starts as `[]` in both config files — populated with `joninasveins@gmail.com` manually, later, after Discord testing succeeds (not part of this plan's tasks).
- The extraction classifier resolves relative date phrases against the email's own `OccurredAt`, never real wall-clock "now" — this is what makes backfill of old mail produce correct dates.
- Past-dated events (compared against real "now" at dispatch time) are dropped silently in `action-svc`'s dispatch handler — nowhere else in the pipeline filters by date.
- This feature's Discord notifier is a **separate, plain `notify.DiscordNotifier`** — explicitly not wrapped by `feign.Gate` or DND. This is a deliberate, spec-approved divergence from every other Discord send in this codebase.
- Each pending event tracks `DiscordStatus` and `CalendarStatus` independently (`"pending"` / `"sent"`, plus `"skipped"` for Calendar only) so a partial failure, or a recipient list filled in after the fact, never re-sends a channel that already succeeded.
- `DueOrOverdue`'s staleness cutoff is 2 days past the event's own date — an event stuck failing longer than that stops being retried.
- New action-svc env vars: `CALENDAR_CLIENT_ID`, `CALENDAR_CLIENT_SECRET`, `CALENDAR_REFRESH_TOKEN` — non-fatal if blank (same posture as `DISCORD_BOT_TOKEN`).

---

### Task 1: Shared config — `SchoolConfig`

**Files:**
- Modify: `common/sharedconfig/config.go`
- Modify: `config/dev.json`
- Modify: `config/prod.json`
- Test: `common/sharedconfig/config_test.go`

**Interfaces:**
- Produces: `sharedconfig.SchoolConfig{Enabled bool, SenderDomains []string, NotifyTime string, CalendarRecipientEmails []string}`, and `Config.School SchoolConfig`. Every later task that reads `school.*` config reads this type.

- [ ] **Step 1: Write the failing tests**

Append to `common/sharedconfig/config_test.go`:

```go
func TestLoad_SchoolFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"watch_paths": ["C:\\a\\errors"],
		"school": {
			"enabled": true,
			"sender_domains": ["@reykjavik.is"],
			"notify_time": "16:00",
			"calendar_recipient_emails": ["joninasveins@gmail.com"]
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.School.Enabled {
		t.Error("School.Enabled = false, want true")
	}
	if len(cfg.School.SenderDomains) != 1 || cfg.School.SenderDomains[0] != "@reykjavik.is" {
		t.Errorf("School.SenderDomains = %v, want [@reykjavik.is]", cfg.School.SenderDomains)
	}
	if cfg.School.NotifyTime != "16:00" {
		t.Errorf("School.NotifyTime = %q, want 16:00", cfg.School.NotifyTime)
	}
	if len(cfg.School.CalendarRecipientEmails) != 1 || cfg.School.CalendarRecipientEmails[0] != "joninasveins@gmail.com" {
		t.Errorf("School.CalendarRecipientEmails = %v, want [joninasveins@gmail.com]", cfg.School.CalendarRecipientEmails)
	}
}

func TestLoad_MissingSchoolField_ZeroValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"watch_paths": []}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := sharedconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.School.Enabled {
		t.Error("School.Enabled = true, want false when school block absent from JSON")
	}
	if len(cfg.School.SenderDomains) != 0 {
		t.Errorf("School.SenderDomains = %v, want empty when school block absent from JSON", cfg.School.SenderDomains)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./common/sharedconfig/... -run TestLoad_SchoolFields -v` (from `common/`)
Expected: FAIL — `cfg.School` is undefined (compile error).

- [ ] **Step 3: Add `SchoolConfig` to `common/sharedconfig/config.go`**

Add this type near `DNDConfig` (after it, before `ClaudeProjectRoot`):

```go
// SchoolConfig holds the school-email-events feature's settings, shared
// between thinking-svc (SenderDomains, Enabled) and action-svc (Enabled,
// NotifyTime, CalendarRecipientEmails). Enabled is false in dev.json and
// true in prod.json — dev and prod poll the same real inbox, so running
// this feature in both would create duplicate Calendar invites and
// duplicate Discord messages. See
// docs/superpowers/specs/2026-09-03-school-email-events-design.md.
type SchoolConfig struct {
	Enabled                 bool     `json:"enabled"`
	SenderDomains           []string `json:"sender_domains"`
	NotifyTime              string   `json:"notify_time"`
	CalendarRecipientEmails []string `json:"calendar_recipient_emails"`
}
```

Add `School SchoolConfig \`json:"school"\`` as a new field on `Config`, after `Web`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./common/sharedconfig/... -v` (from `common/`)
Expected: PASS, all tests including the two new ones.

- [ ] **Step 5: Update `config/dev.json` and `config/prod.json`**

In `config/prod.json`, add after the `"web"` block (before the closing `}`):
```json
,
  "school": {
    "enabled": true,
    "sender_domains": ["@reykjavik.is"],
    "notify_time": "16:00",
    "calendar_recipient_emails": []
  }
```
(i.e. insert `"school": {...}` as a new top-level key; adjust the preceding line's trailing comma accordingly — the `"web"` block currently ends the object.)

In `config/dev.json`, add the same block but with `"enabled": false` and `"sender_domains": []`:
```json
,
  "school": {
    "enabled": false,
    "sender_domains": [],
    "notify_time": "16:00",
    "calendar_recipient_emails": []
  }
```

Validate both files parse: `powershell -Command "Get-Content config/prod.json | ConvertFrom-Json | Out-Null"` and same for `config/dev.json`.

- [ ] **Step 6: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add common/sharedconfig/config.go common/sharedconfig/config_test.go config/dev.json config/prod.json
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(common): add school config block"
```

---

### Task 2: thinking-svc — school-event extraction capability (`llm` package)

**Files:**
- Modify: `thinking-svc/llm/classifier.go`
- Modify: `thinking-svc/llm/deepseek.go`
- Test: `thinking-svc/llm/deepseek_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `llm.SchoolEvent{Date, HasTime bool, Time, Description string}`; `llm.SchoolEventExtractor` interface with `ExtractSchoolEvents(ctx, sender, subject, body string, referenceDate time.Time) (events []SchoolEvent, note string, err error)`; `llm.Client` interface grows to embed it. `*DeepSeekClient` implements it. Task 3's rule and any fake `llm.Client` (Task 3 also updates `thinking-svc/rules/rule_test.go`'s `fakeSummarizer`) depend on this exact signature.

- [ ] **Step 1: Add the type and interface to `thinking-svc/llm/classifier.go`**

Add after the `Classifier` interface, before the `Client` interface:

```go
// SchoolEvent is one actionable school date/time extracted from an email.
// Date is always an absolute "YYYY-MM-DD" — the classifier resolves any
// relative phrase ("this Friday") against the referenceDate it's given,
// not against real wall-clock time, so a backfilled email from weeks ago
// resolves correctly. See
// docs/superpowers/specs/2026-09-03-school-email-events-design.md.
type SchoolEvent struct {
	Date        string
	HasTime     bool
	Time        string // "HH:MM", only meaningful when HasTime
	Description string
}

// SchoolEventExtractor pulls zero or more actionable dates/times out of a
// school email. Mirrors Classifier's fail-closed convention: production
// *DeepSeekClient never returns a non-nil error — any failure collapses to
// (nil events, a "extraction unavailable: ..." note, nil error) so an LLM
// hiccup never blocks the report entry the caller always writes.
type SchoolEventExtractor interface {
	ExtractSchoolEvents(ctx context.Context, sender, subject, body string, referenceDate time.Time) (events []SchoolEvent, note string, err error)
}
```

Add `"time"` to the file's imports (currently only `"context"`).

Change the `Client` interface to:

```go
type Client interface {
	Summarizer
	Classifier
	SchoolEventExtractor
}
```

Add the prompt constant at the end of the file:

```go
// schoolEventExtractorSystemPrompt is a plain string constant, same
// tuning posture as classifierSystemPrompt. %s is filled with the
// reference date ("2006-01-02") the caller supplies — see SchoolEvent's
// doc comment for why that's the email's own received date, not real
// "now".
const schoolEventExtractorSystemPrompt = `You extract actionable school dates/times from an email sent by a school or municipal school system. The reference date for resolving relative phrases ("this Friday," "next Tuesday," "tomorrow") is %s — resolve against that date, not any other notion of "today."

Only include events where a child or parent needs to DO something or BE somewhere on a specific date: a special clothing/theme day, a packed-lunch day, a field trip, a meeting, an event with a start time. Do not include general announcements with no specific date, or purely informational content.

If the email is not actually about a school date (e.g. an unrelated municipal notice) or contains no such actionable date, return an empty array.

Respond with strict JSON only, no markdown, exactly this shape: {"events": [{"date": "YYYY-MM-DD", "has_time": true or false, "time": "HH:MM" or "", "description": "<short phrase>"}]}`
```

- [ ] **Step 2: Write the failing tests**

Append to `thinking-svc/llm/deepseek_test.go`:

```go
func TestDeepSeekClient_ExtractSchoolEvents_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"events\":[{\"date\":\"2026-09-04\",\"has_time\":false,\"time\":\"\",\"description\":\"Sweater day\"}]}"}}]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	ref := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	events, note, err := client.ExtractSchoolEvents(context.Background(), "teacher@reykjavik.is", "Reminder", "Don't forget tomorrow is sweater day!", ref)
	if err != nil {
		t.Fatalf("ExtractSchoolEvents: %v", err)
	}
	if note != "" {
		t.Errorf("note = %q, want empty on success", note)
	}
	if len(events) != 1 {
		t.Fatalf("events = %v, want 1 entry", events)
	}
	if events[0].Date != "2026-09-04" || events[0].HasTime || events[0].Description != "Sweater day" {
		t.Errorf("events[0] = %+v, want {2026-09-04 false \"\" Sweater day}", events[0])
	}
}

func TestDeepSeekClient_ExtractSchoolEvents_EmptyArray_NoEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"events\":[]}"}}]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	events, _, err := client.ExtractSchoolEvents(context.Background(), "info@reykjavik.is", "Notice", "Unrelated municipal notice.", time.Now())
	if err != nil {
		t.Fatalf("ExtractSchoolEvents: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events = %v, want empty", events)
	}
}

func TestDeepSeekClient_ExtractSchoolEvents_NonOKStatus_FailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	events, note, err := client.ExtractSchoolEvents(context.Background(), "a@reykjavik.is", "s", "b", time.Now())
	if err != nil {
		t.Fatalf("ExtractSchoolEvents must never return an error, got: %v", err)
	}
	if events != nil {
		t.Errorf("events = %v, want nil on failure", events)
	}
	if note == "" {
		t.Error("note = \"\", want a non-empty failure explanation")
	}
}

func TestDeepSeekClient_ExtractSchoolEvents_ReferenceDateInPrompt(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"events\":[]}"}}]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	ref := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	client.ExtractSchoolEvents(context.Background(), "a@reykjavik.is", "s", "b", ref)

	messages, _ := capturedBody["messages"].([]interface{})
	if len(messages) == 0 {
		t.Fatal("no messages captured")
	}
	system, _ := messages[0].(map[string]interface{})
	content, _ := system["content"].(string)
	if !strings.Contains(content, "2026-08-05") {
		t.Errorf("system prompt = %q, want it to contain the reference date 2026-08-05", content)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./thinking-svc/llm/... -run TestDeepSeekClient_ExtractSchoolEvents -v` (from `thinking-svc/`)
Expected: FAIL — `ExtractSchoolEvents` undefined on `*DeepSeekClient` (compile error).

- [ ] **Step 4: Implement `ExtractSchoolEvents` in `thinking-svc/llm/deepseek.go`**

Append to the file:

```go
type extractEventsResponse struct {
	Events []schoolEventJSON `json:"events"`
}

type schoolEventJSON struct {
	Date        string `json:"date"`
	HasTime     bool   `json:"has_time"`
	Time        string `json:"time"`
	Description string `json:"description"`
}

// ExtractSchoolEvents sends a single non-streaming Chat Completions request
// asking for actionable school dates/times. Like ClassifyImportance, this
// never returns a non-nil error — any failure (network, non-200, malformed
// response) collapses to (nil events, a "extraction unavailable: ..." note,
// nil error).
func (c *DeepSeekClient) ExtractSchoolEvents(ctx context.Context, sender, subject, body string, referenceDate time.Time) ([]SchoolEvent, string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	systemPrompt := fmt.Sprintf(schoolEventExtractorSystemPrompt, referenceDate.Format("2006-01-02"))
	userMsg := fmt.Sprintf("From: %s\nSubject: %s\n\n%s", sender, subject, body)

	reqBody, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMsg},
		},
		Stream: false,
	})
	if err != nil {
		return nil, fmt.Sprintf("extraction unavailable: marshal request: %v", err), nil
	}

	url := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Sprintf("extraction unavailable: build request: %v", err), nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Sprintf("extraction unavailable: request failed: %v", err), nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Sprintf("extraction unavailable: read response: %v", err), nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Sprintf("extraction unavailable: deepseek status %d", resp.StatusCode), nil
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil || len(parsed.Choices) == 0 {
		return nil, "extraction unavailable: empty or malformed deepseek response", nil
	}

	var result extractEventsResponse
	if err := json.Unmarshal([]byte(parsed.Choices[0].Message.Content), &result); err != nil {
		return nil, "extraction unavailable: non-JSON extractor response", nil
	}

	events := make([]SchoolEvent, len(result.Events))
	for i, e := range result.Events {
		events[i] = SchoolEvent{Date: e.Date, HasTime: e.HasTime, Time: e.Time, Description: e.Description}
	}
	return events, "", nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./thinking-svc/llm/... -v` (from `thinking-svc/`)
Expected: PASS, all tests.

- [ ] **Step 6: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add thinking-svc/llm/classifier.go thinking-svc/llm/deepseek.go thinking-svc/llm/deepseek_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(thinking-svc): add school event extraction to the LLM client"
```

---

### Task 3: thinking-svc — `SchoolEmailRule`

**Files:**
- Create: `thinking-svc/rules/school_email.go`
- Create: `thinking-svc/rules/school_email_test.go`
- Modify: `thinking-svc/rules/rule_test.go` (extend the shared `fakeSummarizer` to satisfy the grown `llm.Client` interface — this file is shared by every rule's test file, so this is the only fake that needs updating)

**Interfaces:**
- Consumes: `llm.Client.ExtractSchoolEvents` (Task 2), `rules.Rule` (existing, from `rule.go`).
- Produces: `rules.NewSchoolEmailRule(senderDomains []string) Rule` — a constructor (unlike the package-var `GmailTriageRule`), consumed by Task 4's `main.go` wiring. Also produces `action_hint: "process_school_event"` with the `Parameters` JSON shape Task 7's dispatch handler unmarshals: `{sender, subject, body_excerpt, note, thread_id, occurred_at, events: [{date, has_time, time, description}]}`.

- [ ] **Step 1: Extend the shared fake in `thinking-svc/rules/rule_test.go`**

Add fields to `fakeSummarizer` and a new method:

```go
type fakeSummarizer struct {
	summary string
	err     error

	classifyImportant bool
	classifyReason    string
	classifyErr       error

	extractEvents []llm.SchoolEvent
	extractNote   string
	extractErr    error
}
```

Add `"soulman/thinking-svc/llm"` to imports, and this method after `ClassifyImportance`:

```go
func (f *fakeSummarizer) ExtractSchoolEvents(_ context.Context, _, _, _ string, _ time.Time) ([]llm.SchoolEvent, string, error) {
	return f.extractEvents, f.extractNote, f.extractErr
}
```

- [ ] **Step 2: Run existing tests to confirm nothing broke**

Run: `go test ./thinking-svc/rules/... -v` (from `thinking-svc/`)
Expected: PASS, all existing tests (this step only adds a method; no behavior changed).

- [ ] **Step 3: Write the failing tests**

Create `thinking-svc/rules/school_email_test.go`:

```go
package rules_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"soulman/common"
	"soulman/thinking-svc/llm"
	"soulman/thinking-svc/rules"
)

func newSchoolStimulus(sender, subject, body string, occurredAt time.Time) *common.Stimulus {
	channelSpecific, _ := json.Marshal(map[string]string{"subject": subject})
	return &common.Stimulus{
		StimulusID: "school-1",
		Channel:    "gmail",
		ReceivedAt: time.Now().UTC(),
		OccurredAt: &occurredAt,
		Source:     common.Source{Identity: sender},
		Content:    common.Content{RawText: body, ContentType: "text"},
		ChannelMeta: common.ChannelMeta{
			ThreadID:        "thread-9",
			ChannelSpecific: channelSpecific,
		},
	}
}

func TestSchoolEmailRule_Match_ConfiguredDomain(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"})
	s := newSchoolStimulus("teacher@reykjavik.is", "Reminder", "sweater day", time.Now())
	if !rule.Match(s) {
		t.Error("Match = false, want true for a configured domain")
	}
}

func TestSchoolEmailRule_Match_OtherDomain_NoMatch(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"})
	s := newSchoolStimulus("someone@example.com", "Reminder", "sweater day", time.Now())
	if rule.Match(s) {
		t.Error("Match = true, want false for an unconfigured domain")
	}
}

func TestSchoolEmailRule_Match_NonGmailChannel_NoMatch(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"})
	s := newSchoolStimulus("teacher@reykjavik.is", "Reminder", "sweater day", time.Now())
	s.Channel = "cli-note"
	if rule.Match(s) {
		t.Error("Match = true, want false for a non-gmail channel")
	}
}

func TestSchoolEmailRule_Match_CaseInsensitive(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"})
	s := newSchoolStimulus("Teacher@Reykjavik.IS", "Reminder", "sweater day", time.Now())
	if !rule.Match(s) {
		t.Error("Match = false, want true for a case-different domain match")
	}
}

func TestSchoolEmailRule_Handle_EventsFound_HighUrgency(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"})
	occurredAt := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	s := newSchoolStimulus("teacher@reykjavik.is", "Reminder", "Tomorrow is sweater day!", occurredAt)

	client := &fakeSummarizer{extractEvents: []llm.SchoolEvent{{Date: "2026-09-04", HasTime: false, Description: "Sweater day"}}}
	req, err := rule.Handle(context.Background(), s, client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if req.ActionHint != "process_school_event" {
		t.Errorf("ActionHint = %q, want process_school_event", req.ActionHint)
	}
	if req.Urgency != "high" {
		t.Errorf("Urgency = %q, want high when events were found", req.Urgency)
	}

	var params struct {
		Sender      string `json:"sender"`
		Subject     string `json:"subject"`
		BodyExcerpt string `json:"body_excerpt"`
		ThreadID    string `json:"thread_id"`
		Events      []struct {
			Date        string `json:"date"`
			HasTime     bool   `json:"has_time"`
			Description string `json:"description"`
		} `json:"events"`
	}
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params.Sender != "teacher@reykjavik.is" || params.Subject != "Reminder" || params.ThreadID != "thread-9" {
		t.Errorf("params = %+v, want sender/subject/thread_id to match", params)
	}
	if len(params.Events) != 1 || params.Events[0].Date != "2026-09-04" || params.Events[0].Description != "Sweater day" {
		t.Errorf("params.Events = %+v, want 1 event 2026-09-04 Sweater day", params.Events)
	}
}

func TestSchoolEmailRule_Handle_NoEvents_NormalUrgency(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"})
	s := newSchoolStimulus("info@reykjavik.is", "Notice", "unrelated municipal notice", time.Now())

	client := &fakeSummarizer{extractEvents: nil}
	req, err := rule.Handle(context.Background(), s, client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if req.Urgency != "normal" {
		t.Errorf("Urgency = %q, want normal when no events were found", req.Urgency)
	}

	var params struct {
		Events []struct{} `json:"events"`
	}
	json.Unmarshal(req.Parameters, &params)
	if len(params.Events) != 0 {
		t.Errorf("params.Events = %v, want empty", params.Events)
	}
}

func TestSchoolEmailRule_Handle_ExtractorError_NoteRecordedNoEvents(t *testing.T) {
	rule := rules.NewSchoolEmailRule([]string{"@reykjavik.is"})
	s := newSchoolStimulus("teacher@reykjavik.is", "Reminder", "sweater day", time.Now())

	client := &fakeSummarizer{extractNote: "extraction unavailable: deepseek status 500"}
	req, err := rule.Handle(context.Background(), s, client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var params struct {
		Note   string     `json:"note"`
		Events []struct{} `json:"events"`
	}
	json.Unmarshal(req.Parameters, &params)
	if params.Note != "extraction unavailable: deepseek status 500" {
		t.Errorf("params.Note = %q, want the extractor's note", params.Note)
	}
	if len(params.Events) != 0 {
		t.Errorf("params.Events = %v, want empty on extractor failure", params.Events)
	}
}

func TestMatch_FindsSchoolEmailRule_WhenPrepended(t *testing.T) {
	orig := rules.Registry
	defer func() { rules.Registry = orig }()
	rules.Registry = append([]rules.Rule{rules.NewSchoolEmailRule([]string{"@reykjavik.is"})}, rules.Registry...)

	s := newSchoolStimulus("teacher@reykjavik.is", "Reminder", "sweater day", time.Now())
	r := rules.Match(s)
	if r == nil || r.Name != "school-email" {
		t.Errorf("Match found %v, want the school-email rule", r)
	}
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `go test ./thinking-svc/rules/... -run TestSchoolEmailRule -v` (from `thinking-svc/`)
Expected: FAIL — `rules.NewSchoolEmailRule` undefined (compile error).

- [ ] **Step 5: Implement `thinking-svc/rules/school_email.go`**

```go
package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"soulman/common"
	"soulman/thinking-svc/llm"
)

// schoolEventParam mirrors action-svc/dispatch's SchoolEventParam — kept as
// a separate type (not shared across modules) per this repo's "small
// independent duplication over cross-module imports" precedent.
type schoolEventParam struct {
	Date        string `json:"date"`
	HasTime     bool   `json:"has_time"`
	Time        string `json:"time"`
	Description string `json:"description"`
}

type schoolEmailParams struct {
	Sender      string             `json:"sender"`
	Subject     string             `json:"subject"`
	BodyExcerpt string             `json:"body_excerpt"`
	Note        string             `json:"note"`
	ThreadID    string             `json:"thread_id"`
	OccurredAt  *time.Time         `json:"occurred_at"`
	Events      []schoolEventParam `json:"events"`
}

// schoolExtractTruncateLen mirrors gmail_triage.go's classifyBodyTruncateLen.
const schoolExtractTruncateLen = 4000

// NewSchoolEmailRule builds a Rule matching gmail stimuli whose sender
// address ends in one of senderDomains (case-insensitive). Constructed
// (not a package var like GmailTriageRule) because the domain allowlist is
// config-driven — see
// docs/superpowers/specs/2026-09-03-school-email-events-design.md.
func NewSchoolEmailRule(senderDomains []string) Rule {
	domains := make([]string, len(senderDomains))
	for i, d := range senderDomains {
		domains[i] = strings.ToLower(d)
	}
	return Rule{
		Name: "school-email",
		Match: func(s *common.Stimulus) bool {
			if s.Channel != "gmail" {
				return false
			}
			sender := strings.ToLower(s.Source.Identity)
			for _, d := range domains {
				if d != "" && strings.HasSuffix(sender, d) {
					return true
				}
			}
			return false
		},
		Handle: handleSchoolEmail,
	}
}

func handleSchoolEmail(ctx context.Context, s *common.Stimulus, client llm.Client) (*common.ActionRequest, error) {
	sender := s.Source.Identity
	subject := gmailSubject(s)
	body := s.Content.RawText
	threadID := s.ChannelMeta.ThreadID

	referenceDate := time.Now()
	if s.OccurredAt != nil {
		referenceDate = *s.OccurredAt
	}

	events, note, err := client.ExtractSchoolEvents(ctx, sender, subject, truncate(body, schoolExtractTruncateLen), referenceDate)
	if err != nil {
		events = nil
		note = fmt.Sprintf("extraction unavailable: %v", err)
	}

	eventParams := make([]schoolEventParam, len(events))
	for i, e := range events {
		eventParams[i] = schoolEventParam{Date: e.Date, HasTime: e.HasTime, Time: e.Time, Description: e.Description}
	}

	params, err := json.Marshal(schoolEmailParams{
		Sender:      sender,
		Subject:     subject,
		BodyExcerpt: truncate(body, excerptLen),
		Note:        note,
		ThreadID:    threadID,
		OccurredAt:  s.OccurredAt,
		Events:      eventParams,
	})
	if err != nil {
		return nil, fmt.Errorf("rules: marshal school email parameters: %w", err)
	}

	intent := "Log this school email to today's daily report"
	urgency := "normal"
	if len(events) > 0 {
		intent = "Log this school email and schedule a parent reminder for its date(s)"
		urgency = "high"
	}

	return &common.ActionRequest{
		CorrelationID:   uuid.NewString(),
		Intent:          intent,
		ActionHint:      "process_school_event",
		Parameters:      params,
		RiskLevel:       "low",
		Urgency:         urgency,
		ExpectedOutcome: "one report entry appended; any future-dated event is queued for a reminder (Discord to the owner, calendar invite to the other parent) at the configured notify time the day before",
		Fallback:        "if report append fails, retry once, then give up silently (same as gmail triage). If an event fails to queue, it is dropped for this run.",
	}, nil
}
```

`excerptLen` and `truncate` are already defined in `thinking-svc/rules/gmail_triage.go` (same package) — no need to redefine them.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./thinking-svc/rules/... -v` (from `thinking-svc/`)
Expected: PASS, all tests including the new ones.

- [ ] **Step 7: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add thinking-svc/rules/school_email.go thinking-svc/rules/school_email_test.go thinking-svc/rules/rule_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(thinking-svc): add SchoolEmailRule"
```

---

### Task 4: thinking-svc — config + prod-only registry gating

**Files:**
- Modify: `thinking-svc/config/config.go`
- Modify: `thinking-svc/main.go`
- Test: `thinking-svc/config/config_test.go`

**Interfaces:**
- Consumes: `sharedconfig.SchoolConfig` (Task 1), `rules.NewSchoolEmailRule` (Task 3).
- Produces: `config.Config.SchoolEnabled bool`, `config.Config.SchoolSenderDomains []string`.

- [ ] **Step 1: Write the failing test**

Append to `thinking-svc/config/config_test.go`. The file's existing `writeConfigFile` helper only marshals the subset of shared-config fields this service already cared about (no `school` block) — rather than widen that helper's signature and touch every existing call site, write the config JSON directly for this test, same as `common/sharedconfig/config_test.go` itself does:

```go
func TestLoad_SchoolFields(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"nats_url": "nats://localhost:4222",
		"stimulus_subject": "soulman.stimulus.raw",
		"thinking_request_subject": "soulman.thinking.request",
		"consumer_names": {"thinking_svc": "thinking-svc"},
		"school": {
			"enabled": true,
			"sender_domains": ["@reykjavik.is"]
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	os.Setenv("CONFIG_PATH", path)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.SchoolEnabled {
		t.Error("SchoolEnabled = false, want true")
	}
	if len(cfg.SchoolSenderDomains) != 1 || cfg.SchoolSenderDomains[0] != "@reykjavik.is" {
		t.Errorf("SchoolSenderDomains = %v, want [@reykjavik.is]", cfg.SchoolSenderDomains)
	}
}

func TestLoad_MissingSchoolBlock_DefaultsDisabled(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.stimulus.raw", "soulman.thinking.request", "thinking-svc")
	os.Setenv("CONFIG_PATH", configPath)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SchoolEnabled {
		t.Error("SchoolEnabled = true, want false when school block absent from config")
	}
	if len(cfg.SchoolSenderDomains) != 0 {
		t.Errorf("SchoolSenderDomains = %v, want empty when school block absent from config", cfg.SchoolSenderDomains)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./thinking-svc/config/... -run TestLoad_SchoolFields -v` (from `thinking-svc/`)
Expected: FAIL — `cfg.SchoolEnabled` undefined (compile error).

- [ ] **Step 3: Add fields to `thinking-svc/config/config.go`**

Add to the `Config` struct:
```go
SchoolEnabled       bool
SchoolSenderDomains []string
```

Add to the returned `&Config{...}` in `Load`:
```go
SchoolEnabled:       shared.School.Enabled,
SchoolSenderDomains: shared.School.SenderDomains,
```

No fatal validation — `school.enabled: false` (dev's default) is a normal, non-error state.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./thinking-svc/config/... -v` (from `thinking-svc/`)
Expected: PASS.

- [ ] **Step 5: Wire the conditional registry prepend into `thinking-svc/main.go`**

In `main()`, after `cfg, err := config.Load()` succeeds and before `handler := &stimulusHandler{...}`, add:

```go
if cfg.SchoolEnabled && len(cfg.SchoolSenderDomains) > 0 {
    rules.Registry = append([]rules.Rule{rules.NewSchoolEmailRule(cfg.SchoolSenderDomains)}, rules.Registry...)
    slog.Info("school-email rule enabled", "sender_domains", cfg.SchoolSenderDomains)
} else {
    slog.Info("school-email rule disabled", "enabled", cfg.SchoolEnabled, "sender_domains_count", len(cfg.SchoolSenderDomains))
}
```

`"soulman/thinking-svc/rules"` is already imported in `main.go`.

- [ ] **Step 6: Build and run the full thinking-svc suite**

Run: `go build ./... && go test ./... -v` (from `thinking-svc/`)
Expected: builds clean, all tests pass.

- [ ] **Step 7: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add thinking-svc/config/config.go thinking-svc/config/config_test.go thinking-svc/main.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(thinking-svc): gate SchoolEmailRule behind school.enabled config"
```

---

### Task 5: action-svc — pending-event store (`schoolevents` package)

**Files:**
- Create: `action-svc/schoolevents/store.go`
- Create: `action-svc/schoolevents/store_test.go`

**Interfaces:**
- Produces: `schoolevents.Event{ID, Date, HasTime, Time, Description, Sender, Subject, DiscordStatus, CalendarStatus string, CreatedAt time.Time}`; `schoolevents.ID(threadID string, index int, date string) string`; `schoolevents.Save(root string, e Event) error`; `schoolevents.MarkDiscordSent(root, id string) error`; `schoolevents.MarkCalendarStatus(root, id, status string) error`; `schoolevents.DueOrOverdue(root string, now time.Time) ([]Event, error)`. Consumed by Task 7 (dispatch) and Task 8 (scheduler).

- [ ] **Step 1: Write the failing tests**

Create `action-svc/schoolevents/store_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./action-svc/schoolevents/... -v` (from `action-svc/`)
Expected: FAIL — package doesn't exist (compile error).

- [ ] **Step 3: Implement `action-svc/schoolevents/store.go`**

```go
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
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", threadID, index, date)))
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

	tomorrow := now.AddDate(0, 0, 1)
	cutoff := now.AddDate(0, 0, -staleCutoffDays)

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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./action-svc/schoolevents/... -v` (from `action-svc/`)
Expected: PASS, all tests.

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add action-svc/schoolevents/store.go action-svc/schoolevents/store_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(action-svc): add schoolevents pending-reminder store"
```

---

### Task 6: action-svc — Calendar client (`calendar` package)

**Files:**
- Create: `action-svc/calendar/calendar.go`
- Create: `action-svc/calendar/calendar_test.go`
- Modify: `action-svc/go.mod` (adds `golang.org/x/oauth2`, `google.golang.org/api`)

**Interfaces:**
- Produces: `calendar.Invite{Summary, Description, Date string, HasTime bool, Time string, Attendees []string}`; `calendar.Client` with `New(ctx, clientID, clientSecret, refreshToken string) (*Client, error)` and `(*Client).CreateInvite(ctx, inv Invite) error`. Consumed by Task 8 (scheduler) and Task 9 (main.go wiring).

- [ ] **Step 1: Add dependencies**

Run (from `action-svc/`): `go get golang.org/x/oauth2@v0.36.0 google.golang.org/api@v0.289.0` (pin the same versions `perception-svc/go.mod` already uses, for consistency), then `go mod tidy`.

- [ ] **Step 2: Write the failing tests**

Create `action-svc/calendar/calendar_test.go`. This tests the event-shape construction logic directly (no live Google account, no HTTP — the same "pure function, testable against fixtures" split `gmailwatcher` uses), so factor the `Invite`→`*calendar.Event` mapping into its own unexported function `toCalendarEvent` that the test calls directly:

```go
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
```

This is an internal test (`package calendar`, not `calendar_test`) since it exercises the unexported `toCalendarEvent`.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./action-svc/calendar/... -v` (from `action-svc/`)
Expected: FAIL — `toCalendarEvent`/`Invite` undefined (compile error).

- [ ] **Step 4: Implement `action-svc/calendar/calendar.go`**

```go
// Package calendar sends Google Calendar invites for school-event
// reminders. OAuth2 bootstrap mirrors
// perception-svc/gmailwatcher/client.go's constructor exactly (offline
// refresh token, golang.org/x/oauth2/google, Production app status), but
// against the Calendar API with the calendar.events scope instead of
// Gmail's. See
// docs/superpowers/specs/2026-09-03-school-email-events-design.md.
package calendar

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gcal "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

const calendarEventsScope = "https://www.googleapis.com/auth/calendar.events"

// Invite is one reminder to send as a Calendar event. No end time is ever
// supplied by the source email — timed events get a fixed 1-hour duration.
type Invite struct {
	Summary     string
	Description string
	Date        string // "YYYY-MM-DD"
	HasTime     bool
	Time        string // "HH:MM" local, only meaningful when HasTime
	Attendees   []string
}

type Client struct {
	svc *gcal.Service
}

// New builds an OAuth2 token source from clientID/clientSecret and a
// long-lived refresh token, then constructs a Calendar API client using
// it — same silent-refresh, no-interactive-reconsent shape as
// gmailwatcher.newRealGmailClient.
func New(ctx context.Context, clientID, clientSecret, refreshToken string) (*Client, error) {
	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{calendarEventsScope},
	}
	tokenSource := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	httpClient := oauth2.NewClient(ctx, tokenSource)

	svc, err := gcal.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("calendar: build calendar service: %w", err)
	}
	return &Client{svc: svc}, nil
}

// CreateInvite creates a primary-calendar event with sendUpdates="all" so
// every attendee gets a real invite email/notification.
func (c *Client) CreateInvite(ctx context.Context, inv Invite) error {
	ev := toCalendarEvent(inv)
	_, err := c.svc.Events.Insert("primary", ev).SendUpdates("all").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("calendar: create event %q: %w", inv.Summary, err)
	}
	return nil
}

// toCalendarEvent maps an Invite to the Calendar API's Event shape. Pure
// function — no network calls — so it's testable without a live account,
// mirroring gmailwatcher/stimulus.go's buildStimulus split. Date-only
// events use Start.Date/End.Date (all-day, end exclusive = Date+1 day) per
// the Calendar API's own convention. Timed events use
// Start.DateTime/End.DateTime with a fixed 1-hour duration.
func toCalendarEvent(inv Invite) *gcal.Event {
	attendees := make([]*gcal.EventAttendee, len(inv.Attendees))
	for i, email := range inv.Attendees {
		attendees[i] = &gcal.EventAttendee{Email: email}
	}

	ev := &gcal.Event{
		Summary:     inv.Summary,
		Description: inv.Description,
		Attendees:   attendees,
	}

	if !inv.HasTime {
		date, err := time.Parse("2006-01-02", inv.Date)
		if err != nil {
			date = time.Now()
		}
		ev.Start = &gcal.EventDateTime{Date: date.Format("2006-01-02")}
		ev.End = &gcal.EventDateTime{Date: date.AddDate(0, 0, 1).Format("2006-01-02")}
		return ev
	}

	start, err := time.ParseInLocation("2006-01-02 15:04", inv.Date+" "+inv.Time, time.Local)
	if err != nil {
		start = time.Now()
	}
	end := start.Add(1 * time.Hour)
	ev.Start = &gcal.EventDateTime{DateTime: start.Format(time.RFC3339)}
	ev.End = &gcal.EventDateTime{DateTime: end.Format(time.RFC3339)}
	return ev
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go build ./... && go test ./action-svc/calendar/... -v` (from `action-svc/`)
Expected: builds clean, all tests pass.

- [ ] **Step 6: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add action-svc/calendar/calendar.go action-svc/calendar/calendar_test.go action-svc/go.mod action-svc/go.sum
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(action-svc): add Google Calendar invite client"
```

---

### Task 7: action-svc — `process_school_event` dispatch handler

**Files:**
- Create: `action-svc/dispatch/school_event.go`
- Create: `action-svc/dispatch/school_event_test.go`
- Modify: `action-svc/dispatch/dispatch.go` (add the switch case)

**Interfaces:**
- Consumes: `schoolevents.Save`/`ID` (Task 5), `report.Append` (existing), `Dispatcher` (existing, from `dispatch.go`).
- Produces: `dispatchSchoolEvent` wired into `Handle`'s switch on `action_hint: "process_school_event"`. Uses the exact `Parameters` JSON shape Task 3's rule produces.

- [ ] **Step 1: Write the failing tests**

Create `action-svc/dispatch/school_event_test.go`:

```go
package dispatch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"soulman/action-svc/dispatch"
	"soulman/action-svc/schoolevents"
	"soulman/common"
)

func schoolEventParamsJSON(t *testing.T, sender, subject string, occurredAt string, events []map[string]interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]interface{}{
		"sender":       sender,
		"subject":      subject,
		"body_excerpt": "excerpt text",
		"note":         "",
		"thread_id":    "thread-9",
		"occurred_at":  occurredAt,
		"events":       events,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return b
}

func TestDispatch_SchoolEvent_FutureDate_QueuesEvent(t *testing.T) {
	root := t.TempDir()
	pub := &fakePublisher{}
	d := dispatch.New(root, pub, nil, nil)

	future := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	req := common.ActionRequest{
		CorrelationID: "s1",
		ActionHint:    "process_school_event",
		Parameters: schoolEventParamsJSON(t, "teacher@reykjavik.is", "Reminder", time.Now().Format(time.RFC3339),
			[]map[string]interface{}{{"date": future, "has_time": false, "time": "", "description": "Sweater day"}}),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	id := schoolevents.ID("thread-9", 0, future)
	entries, err := os.ReadDir(filepath.Join(root, "logs", "school-events"))
	if err != nil {
		t.Fatalf("read school-events dir: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name() == id+".json" {
			found = true
		}
	}
	if !found {
		t.Errorf("no pending event file found for id %s among %v", id, entries)
	}
}

func TestDispatch_SchoolEvent_PastDate_DroppedSilently(t *testing.T) {
	root := t.TempDir()
	pub := &fakePublisher{}
	d := dispatch.New(root, pub, nil, nil)

	past := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	req := common.ActionRequest{
		CorrelationID: "s2",
		ActionHint:    "process_school_event",
		Parameters: schoolEventParamsJSON(t, "teacher@reykjavik.is", "Reminder", time.Now().Format(time.RFC3339),
			[]map[string]interface{}{{"date": past, "has_time": false, "time": "", "description": "Already happened"}}),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	entries, err := os.ReadDir(filepath.Join(root, "logs", "school-events"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read school-events dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("school-events dir = %v, want empty for a past-dated event", entries)
	}
}

func TestDispatch_SchoolEvent_NoEvents_ReportOnlyNotImportant(t *testing.T) {
	root := t.TempDir()
	pub := &fakePublisher{}
	d := dispatch.New(root, pub, nil, nil)

	req := common.ActionRequest{
		CorrelationID: "s3",
		ActionHint:    "process_school_event",
		Parameters:    schoolEventParamsJSON(t, "info@reykjavik.is", "Notice", time.Now().Format(time.RFC3339), nil),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	rec, ok := pub.last()
	if !ok || rec.Decision != "logged only" {
		t.Errorf("outcome = %+v, ok=%v, want Decision=logged only", rec, ok)
	}
}

func TestDispatch_SchoolEvent_EventsFound_OutcomeReflectsCount(t *testing.T) {
	root := t.TempDir()
	pub := &fakePublisher{}
	d := dispatch.New(root, pub, nil, nil)

	future := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	req := common.ActionRequest{
		CorrelationID: "s4",
		ActionHint:    "process_school_event",
		Parameters: schoolEventParamsJSON(t, "teacher@reykjavik.is", "Reminder", time.Now().Format(time.RFC3339),
			[]map[string]interface{}{{"date": future, "has_time": false, "time": "", "description": "Sweater day"}}),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	rec, ok := pub.last()
	if !ok || rec.Decision != "1 event(s) queued" {
		t.Errorf("outcome = %+v, ok=%v, want Decision=\"1 event(s) queued\"", rec, ok)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./action-svc/dispatch/... -run TestDispatch_SchoolEvent -v` (from `action-svc/`)
Expected: FAIL — `process_school_event` unhandled / compile error against `schoolevents` import not yet used anywhere in `dispatch`.

- [ ] **Step 3: Implement `action-svc/dispatch/school_event.go`**

```go
package dispatch

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"soulman/action-svc/report"
	"soulman/action-svc/schoolevents"
	"soulman/common"
)

// SchoolEventParam mirrors thinking-svc's schoolEventParam — the shape one
// extracted event takes inside process_school_event's Parameters.
type SchoolEventParam struct {
	Date        string `json:"date"`
	HasTime     bool   `json:"has_time"`
	Time        string `json:"time"`
	Description string `json:"description"`
}

// SchoolEmailParams mirrors thinking-svc's schoolEmailParams — the
// Parameters shape process_school_event Action Requests carry.
type SchoolEmailParams struct {
	Sender      string             `json:"sender"`
	Subject     string             `json:"subject"`
	BodyExcerpt string             `json:"body_excerpt"`
	Note        string             `json:"note"`
	ThreadID    string             `json:"thread_id"`
	OccurredAt  string             `json:"occurred_at"`
	Events      []SchoolEventParam `json:"events"`
}

func (d *Dispatcher) dispatchSchoolEvent(req common.ActionRequest) {
	var p SchoolEmailParams
	if err := json.Unmarshal(req.Parameters, &p); err != nil {
		slog.Error("dispatch: process_school_event unparseable params, dropping", "correlation_id", req.CorrelationID, "error", err)
		return
	}

	occurredAt, parseErr := time.Parse(time.RFC3339, p.OccurredAt)
	if parseErr != nil {
		occurredAt = time.Now()
	}

	important := len(p.Events) > 0
	entry := report.Entry{
		Summary:    fmt.Sprintf("%s — %d school event(s) found", p.Subject, len(p.Events)),
		RawContent: fmt.Sprintf("Note: %s\n\n%s", p.Note, p.BodyExcerpt),
		SourcePath: p.Sender + "/" + p.ThreadID,
		OccurredAt: occurredAt.Local(),
		Important:  important,
	}
	_, err := report.Append(d.root, entry)
	if err != nil {
		slog.Warn("dispatch: process_school_event report append failed, retrying once", "correlation_id", req.CorrelationID, "error", err)
		_, err = report.Append(d.root, entry)
	}
	status := "success"
	if err != nil {
		status = "failed"
		slog.Error("dispatch: process_school_event report append failed after retry, giving up", "correlation_id", req.CorrelationID, "error", err)
	}

	now := time.Now()
	queued := 0
	for i, ev := range p.Events {
		date, parseErr := time.ParseInLocation("2006-01-02", ev.Date, now.Location())
		if parseErr != nil {
			slog.Warn("dispatch: process_school_event unparseable event date, dropping", "date", ev.Date, "correlation_id", req.CorrelationID)
			continue
		}
		if date.Before(startOfDay(now)) {
			continue // already happened — silently dropped, no retry
		}

		id := schoolevents.ID(p.ThreadID, i, ev.Date)
		saveErr := schoolevents.Save(d.root, schoolevents.Event{
			ID: id, Date: ev.Date, HasTime: ev.HasTime, Time: ev.Time, Description: ev.Description,
			Sender: p.Sender, Subject: p.Subject,
			DiscordStatus: "pending", CalendarStatus: "pending", CreatedAt: now,
		})
		if saveErr != nil {
			slog.Error("dispatch: process_school_event failed to queue event", "id", id, "error", saveErr)
			continue
		}
		queued++
	}

	if d.publisher == nil {
		return
	}

	decision := "logged only"
	if queued > 0 {
		decision = fmt.Sprintf("%d event(s) queued", queued)
	}

	rec := common.OutcomeRecord{
		ActionType: req.ActionHint,
		Status:     status,
		TaskID:     req.CorrelationID,
		OccurredAt: occurredAt,
		Summary:    entry.Summary,
		Decision:   decision,
		Tags:       []string{"gmail", "school"},
	}
	if pubErr := d.publisher.PublishOutcome(rec); pubErr != nil {
		slog.Error("dispatch: outcome publish failed", "correlation_id", req.CorrelationID, "error", pubErr)
	}
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
```

- [ ] **Step 4: Wire the switch case in `action-svc/dispatch/dispatch.go`**

Add a case to the existing `switch req.ActionHint` in `Handle`:
```go
case "process_school_event":
    d.dispatchSchoolEvent(req)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./action-svc/dispatch/... -v` (from `action-svc/`)
Expected: PASS, all tests including the new ones.

- [ ] **Step 6: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add action-svc/dispatch/school_event.go action-svc/dispatch/school_event_test.go action-svc/dispatch/dispatch.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(action-svc): add process_school_event dispatch handler"
```

---

### Task 8: action-svc — `SchoolEventScheduler`

**Files:**
- Create: `action-svc/scheduler/school.go`
- Create: `action-svc/scheduler/school_test.go`

**Interfaces:**
- Consumes: `schoolevents.DueOrOverdue`/`MarkDiscordSent`/`MarkCalendarStatus` (Task 5), `calendar.Invite`/`calendar.Client` structural shape (Task 6, via a local `EventInviter` interface), `notify.Notifier` (existing).
- Produces: `scheduler.NewSchoolEventScheduler(root, notifyTime string, recipients []string, discordNotifier notify.Notifier, inviter EventInviter) *SchoolEventScheduler` with `Start()`/`Stop()`/`RunOnce()`. Consumed by Task 9's `main.go` wiring.

- [ ] **Step 1: Write the failing tests**

Create `action-svc/scheduler/school_test.go`:

```go
package scheduler_test

import (
	"context"
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./action-svc/scheduler/... -run "TestRunOnce_DueEvent|TestStart_CatchUp" -v` (from `action-svc/`)
Expected: FAIL — `NewSchoolEventScheduler`/`EventInviter` undefined (compile error).

- [ ] **Step 3: Implement `action-svc/scheduler/school.go`**

```go
package scheduler

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
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
// dnd.Flusher (nextRun/time.Until/time.After, overridable Now/BackoffBase).
type SchoolEventScheduler struct {
	root            string
	notifyTime      string
	recipients      []string
	discordNotifier notify.Notifier
	inviter         EventInviter
	stop            chan struct{}

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
		wait := time.Until(s.nextRun(s.Now()))
		select {
		case <-time.After(wait):
			s.RunOnce()
		case <-s.stop:
			return
		}
	}
}

func (s *SchoolEventScheduler) nextRun(from time.Time) time.Time {
	hh, mm := parseSendTime(s.notifyTime)
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

func parseSendTime(s string) (int, int) {
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
```

Note: this file defines its own `parseSendTime` (default `16:00`) rather than reusing `daily.go`'s private `parseSendTime` (default `10:00`, and unexported so it can't be imported anyway) — same small independent duplication precedent used throughout this codebase.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./action-svc/scheduler/... -v` (from `action-svc/`)
Expected: PASS, all tests including the new ones (the catch-up test polls for up to 2s — if it's flaky in CI, that's a real signal to investigate, not to loosen).

- [ ] **Step 5: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add action-svc/scheduler/school.go action-svc/scheduler/school_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(action-svc): add SchoolEventScheduler with startup catch-up"
```

---

### Task 9: action-svc — config + main.go wiring

**Files:**
- Modify: `action-svc/config/config.go`
- Modify: `action-svc/main.go`
- Test: `action-svc/config/config_test.go`

**Interfaces:**
- Consumes: `sharedconfig.SchoolConfig` (Task 1), `calendar.New`/`calendar.Client` (Task 6), `scheduler.NewSchoolEventScheduler` (Task 8).
- Produces: `config.Config` gains `SchoolEnabled bool`, `SchoolNotifyTime string`, `SchoolCalendarRecipientEmails []string`, `CalendarClientID/Secret/RefreshToken string` (env-sourced).

- [ ] **Step 1: Write the failing test**

Append to `action-svc/config/config_test.go` (also add `"CALENDAR_CLIENT_ID"`, `"CALENDAR_CLIENT_SECRET"`, `"CALENDAR_REFRESH_TOKEN"` to `unsetAllEnv`'s existing `os.Unsetenv` calls):

```go
func TestLoad_SchoolFields(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"nats_url": "nats://localhost:4222",
		"thinking_request_subject": "soulman.thinking.request",
		"memory_write_subject": "soulman.memory.write",
		"consumer_names": {"action_svc": "action-svc"},
		"school": {
			"enabled": true,
			"notify_time": "16:00",
			"calendar_recipient_emails": ["joninasveins@gmail.com"]
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("CALENDAR_CLIENT_ID", "id-123")
	os.Setenv("CALENDAR_CLIENT_SECRET", "secret-456")
	os.Setenv("CALENDAR_REFRESH_TOKEN", "refresh-789")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.SchoolEnabled {
		t.Error("SchoolEnabled = false, want true")
	}
	if cfg.SchoolNotifyTime != "16:00" {
		t.Errorf("SchoolNotifyTime = %q, want 16:00", cfg.SchoolNotifyTime)
	}
	if len(cfg.SchoolCalendarRecipientEmails) != 1 || cfg.SchoolCalendarRecipientEmails[0] != "joninasveins@gmail.com" {
		t.Errorf("SchoolCalendarRecipientEmails = %v, want [joninasveins@gmail.com]", cfg.SchoolCalendarRecipientEmails)
	}
	if cfg.CalendarClientID != "id-123" || cfg.CalendarClientSecret != "secret-456" || cfg.CalendarRefreshToken != "refresh-789" {
		t.Errorf("Calendar creds = %q/%q/%q, want id-123/secret-456/refresh-789", cfg.CalendarClientID, cfg.CalendarClientSecret, cfg.CalendarRefreshToken)
	}
}

func TestLoad_SchoolFieldsAbsent_Defaults(t *testing.T) {
	unsetAllEnv()
	defer unsetAllEnv()

	configPath := writeConfigFile(t, "nats://localhost:4222", "soulman.thinking.request", "soulman.memory.write", "action-svc")
	os.Setenv("CONFIG_PATH", configPath)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SchoolEnabled {
		t.Error("SchoolEnabled = true, want false when school block absent")
	}
	if cfg.SchoolNotifyTime != "16:00" {
		t.Errorf("SchoolNotifyTime = %q, want default 16:00 when notify_time absent", cfg.SchoolNotifyTime)
	}
	if len(cfg.SchoolCalendarRecipientEmails) != 0 {
		t.Errorf("SchoolCalendarRecipientEmails = %v, want empty", cfg.SchoolCalendarRecipientEmails)
	}
	if cfg.CalendarClientID != "" || cfg.CalendarClientSecret != "" || cfg.CalendarRefreshToken != "" {
		t.Error("Calendar creds should default to empty strings when env vars are unset")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./action-svc/config/... -v` (from `action-svc/`)
Expected: FAIL — new fields undefined (compile error).

- [ ] **Step 3: Add fields to `action-svc/config/config.go`**

Add to `Config`:
```go
SchoolEnabled                 bool
SchoolNotifyTime              string
SchoolCalendarRecipientEmails []string
CalendarClientID              string
CalendarClientSecret          string
CalendarRefreshToken          string
```

Add to the returned `&Config{...}`:
```go
SchoolEnabled:                 shared.School.Enabled,
SchoolNotifyTime:              orDefault(shared.School.NotifyTime, "16:00"),
SchoolCalendarRecipientEmails: shared.School.CalendarRecipientEmails,
CalendarClientID:              env("CALENDAR_CLIENT_ID", ""),
CalendarClientSecret:          env("CALENDAR_CLIENT_SECRET", ""),
CalendarRefreshToken:          env("CALENDAR_REFRESH_TOKEN", ""),
```

Add this small helper (mirrors the loose-default posture `ReportSendTime`'s `env(..., "10:00")` already has, but for a value that comes from shared config rather than an env var):
```go
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./action-svc/config/... -v` (from `action-svc/`)
Expected: PASS.

- [ ] **Step 5: Wire calendar client + scheduler into `action-svc/main.go`**

After the existing `sched := scheduler.New(...)` / `sched.Start()` / `defer sched.Stop()` block, add:

```go
// School event reminders — prod-only via cfg.SchoolEnabled (see
// docs/superpowers/specs/2026-09-03-school-email-events-design.md).
// Deliberately its own plain Discord notifier, NOT feign- or DND-wrapped —
// this feature ships live from day one.
if cfg.SchoolEnabled {
    schoolDiscord := notify.NewDiscordNotifier(cfg.DiscordBotToken, cfg.DiscordChannelID)

    var inviter scheduler.EventInviter
    if cfg.CalendarClientID != "" && cfg.CalendarClientSecret != "" && cfg.CalendarRefreshToken != "" {
        calClient, calErr := calendar.New(ctx, cfg.CalendarClientID, cfg.CalendarClientSecret, cfg.CalendarRefreshToken)
        if calErr != nil {
            slog.Warn("calendar client init failed — calendar invites will fail until fixed", "error", calErr)
        } else {
            inviter = calClient
        }
    } else {
        slog.Warn("CALENDAR_CLIENT_ID/SECRET/REFRESH_TOKEN not fully set — calendar invites will fail until configured")
    }

    schoolSched := scheduler.NewSchoolEventScheduler(cfg.SoulmanRoot, cfg.SchoolNotifyTime, cfg.SchoolCalendarRecipientEmails, schoolDiscord, inviter)
    schoolSched.Start()
    defer schoolSched.Stop()
}
```

Add `"soulman/action-svc/calendar"` to `main.go`'s imports.

`SchoolEventScheduler.notifyOne` already guards against a nil `inviter` (Task 8's `if len(s.recipients) == 0 || s.inviter == nil { return }`, covered by `TestRunOnce_NilInviter_StaysPending`) — so passing `inviter == nil` here when Calendar credentials aren't yet configured is safe even if `cfg.SchoolCalendarRecipientEmails` is non-empty.

- [ ] **Step 6: Build and run the full action-svc suite**

Run: `go build ./... && go test ./... -v` (from `action-svc/`)
Expected: builds clean, all tests pass.

- [ ] **Step 7: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add action-svc/config/config.go action-svc/config/config_test.go action-svc/main.go action-svc/scheduler/school.go action-svc/scheduler/school_test.go
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(action-svc): wire school event scheduler and calendar client into main"
```

---

### Task 10: `cli` — `school-backfill` subcommand

**Files:**
- Create: `cli/schoolbackfill.go`
- Create: `cli/schoolbackfill_test.go`
- Modify: `cli/args.go` (new `school-backfill` positional command + `--since`/`--dry-run` flags)
- Modify: `cli/main.go` (dispatch the new mode)
- Modify: `cli/go.mod` (adds `golang.org/x/oauth2`, `google.golang.org/api`, and a `soulman/common` replace directive)

**Interfaces:**
- Consumes: `client.SendRaw` (existing, `cli/client/client.go`), `common.Stimulus` (existing).
- Produces: `soulman school-backfill --since YYYY-MM-DD [--dry-run] [--dev]` — targets prod's perception-svc by default (the design's explicit prod-only requirement), same as every other `cli` command's existing `--dev` opt-in convention.

- [ ] **Step 1: Add dependencies**

Run (from `cli/`): `go get golang.org/x/oauth2@v0.36.0 google.golang.org/api@v0.289.0`, then add to `cli/go.mod`:
```
require soulman/common v0.0.0
replace soulman/common => ../common
```
Run `go mod tidy`.

- [ ] **Step 2: Write the failing tests**

Create `cli/schoolbackfill_test.go` — tests the pure Gmail-message-to-Stimulus mapping only (no live Gmail account or HTTP needed, mirroring `gmailwatcher/stimulus_test.go`'s fixture-based approach):

```go
package main

import (
	"testing"
	"time"

	"google.golang.org/api/gmail/v1"
)

func TestBuildBackfillStimulus_MapsCoreFields(t *testing.T) {
	msg := &gmail.Message{
		Id:           "msg-1",
		ThreadId:     "thread-1",
		InternalDate: 1756800000000, // 2025-09-02T08:00:00Z
		Payload: &gmail.MessagePart{
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "teacher@reykjavik.is"},
				{Name: "Subject", Value: "Sweater day reminder"},
			},
			MimeType: "text/plain",
			Body:     &gmail.MessagePartBody{Data: "SGVsbG8="}, // "Hello"
		},
	}

	s, err := buildBackfillStimulus(msg)
	if err != nil {
		t.Fatalf("buildBackfillStimulus: %v", err)
	}
	if s.Channel != "gmail" {
		t.Errorf("Channel = %q, want gmail", s.Channel)
	}
	if s.Source.Identity != "teacher@reykjavik.is" {
		t.Errorf("Source.Identity = %q, want teacher@reykjavik.is", s.Source.Identity)
	}
	if s.Content.RawText != "Hello" {
		t.Errorf("Content.RawText = %q, want Hello", s.Content.RawText)
	}
	if s.ChannelMeta.ThreadID != "thread-1" {
		t.Errorf("ChannelMeta.ThreadID = %q, want thread-1", s.ChannelMeta.ThreadID)
	}
	if s.OccurredAt == nil || !s.OccurredAt.Equal(time.UnixMilli(1756800000000).UTC()) {
		t.Errorf("OccurredAt = %v, want the message's real InternalDate (not time.Now)", s.OccurredAt)
	}
}

func TestBuildBackfillStimulus_NilPayload_Errors(t *testing.T) {
	msg := &gmail.Message{Id: "msg-2"}
	if _, err := buildBackfillStimulus(msg); err == nil {
		t.Error("expected an error for a message with no payload")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./... -run TestBuildBackfillStimulus -v` (from `cli/`)
Expected: FAIL — `buildBackfillStimulus` undefined (compile error).

- [ ] **Step 4: Implement `cli/schoolbackfill.go`**

```go
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/mail"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"

	"soulman/cli/client"
	"soulman/common"
)

// runSchoolBackfill implements `soulman school-backfill --since YYYY-MM-DD
// [--dry-run] [--dev]`: a one-off historical scan of @reykjavik.is mail,
// feeding each match through perception-svc's existing debug-injection
// endpoint (POST /api/perceive/raw) — reusing the live pipeline rather
// than building special-case backfill logic. See
// docs/superpowers/specs/2026-09-03-school-email-events-design.md.
//
// Deliberately duplicates gmailwatcher's OAuth bootstrap and MIME-body
// extraction rather than importing perception-svc directly — cli is a
// separate Go module, and this repo prefers small independent duplication
// over cross-module imports (see action-svc/NOTES.md).
func runSchoolBackfill(baseURL, since string, dryRun bool) error {
	clientID := os.Getenv("GMAIL_CLIENT_ID")
	clientSecret := os.Getenv("GMAIL_CLIENT_SECRET")
	refreshToken := os.Getenv("GMAIL_REFRESH_TOKEN")
	if clientID == "" || clientSecret == "" || refreshToken == "" {
		return fmt.Errorf("GMAIL_CLIENT_ID, GMAIL_CLIENT_SECRET, and GMAIL_REFRESH_TOKEN must all be set in the environment")
	}

	ctx := context.Background()
	svc, err := newGmailService(ctx, clientID, clientSecret, refreshToken)
	if err != nil {
		return fmt.Errorf("build gmail service: %w", err)
	}

	query := fmt.Sprintf("from:*@reykjavik.is after:%s", strings.ReplaceAll(since, "-", "/"))
	var ids []string
	if err := svc.Users.Messages.List("me").Q(query).Pages(ctx, func(resp *gmail.ListMessagesResponse) error {
		for _, m := range resp.Messages {
			ids = append(ids, m.Id)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("list messages: %w", err)
	}

	fmt.Printf("found %d message(s) matching %q\n", len(ids), query)

	injected := 0
	for _, id := range ids {
		msg, err := svc.Users.Messages.Get("me", id).Format("full").Context(ctx).Do()
		if err != nil {
			fmt.Printf("  %s: get failed: %v\n", id, err)
			continue
		}
		s, err := buildBackfillStimulus(msg)
		if err != nil {
			fmt.Printf("  %s: build stimulus failed: %v\n", id, err)
			continue
		}
		body, err := json.Marshal(s)
		if err != nil {
			fmt.Printf("  %s: marshal failed: %v\n", id, err)
			continue
		}
		if dryRun {
			fmt.Printf("  %s: [dry-run] would inject (occurred_at=%s)\n", id, s.OccurredAt.Format(time.RFC3339))
			continue
		}
		stimulusID, err := client.SendRaw(baseURL, body)
		if err != nil {
			fmt.Printf("  %s: inject failed: %v\n", id, err)
			continue
		}
		fmt.Printf("  %s: injected (stimulus_id: %s)\n", id, stimulusID)
		injected++
	}

	if !dryRun {
		fmt.Printf("injected %d/%d message(s)\n", injected, len(ids))
	}
	return nil
}

func newGmailService(ctx context.Context, clientID, clientSecret, refreshToken string) (*gmail.Service, error) {
	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{"https://www.googleapis.com/auth/gmail.readonly"},
	}
	tokenSource := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	httpClient := oauth2.NewClient(ctx, tokenSource)
	return gmail.NewService(ctx, option.WithHTTPClient(httpClient))
}

// buildBackfillStimulus maps a fully-fetched Gmail message into a
// common.Stimulus, setting OccurredAt from the message's real InternalDate
// (not time.Now) — critical so thinking-svc's extraction prompt resolves
// relative date phrases against when the email was actually sent. Only
// text/plain (falling back to text/html) is extracted; unlike
// gmailwatcher's full buildStimulus, attachment metadata is not collected
// here — not needed for extraction, and not worth the duplicated logic.
func buildBackfillStimulus(msg *gmail.Message) (*common.Stimulus, error) {
	if msg.Payload == nil {
		return nil, fmt.Errorf("message %s has no payload", msg.Id)
	}

	headers := map[string]string{}
	for _, h := range msg.Payload.Headers {
		headers[h.Name] = h.Value
	}
	fromAddr := headers["From"]
	if addr, err := mail.ParseAddress(fromAddr); err == nil {
		fromAddr = addr.Address
	}
	subject := headers["Subject"]

	rawText, contentType := extractBackfillBody(msg.Payload)
	occurredAt := time.UnixMilli(msg.InternalDate).UTC()

	channelSpecific, err := json.Marshal(map[string]string{"subject": subject})
	if err != nil {
		return nil, fmt.Errorf("marshal channel_specific: %w", err)
	}

	return &common.Stimulus{
		Channel:    "gmail",
		OccurredAt: &occurredAt,
		Source:     common.Source{Identity: fromAddr, AuthMethod: "none"},
		Content:    common.Content{RawText: rawText, ContentType: contentType},
		ChannelMeta: common.ChannelMeta{
			MessageID:       msg.Id,
			ThreadID:        msg.ThreadId,
			ChannelSpecific: json.RawMessage(channelSpecific),
		},
		Hints: common.Hints{Priority: "normal", Tags: []string{"email", "gmail", "backfill"}},
	}, nil
}

func extractBackfillBody(part *gmail.MessagePart) (text, contentType string) {
	plain, html := findBackfillTextParts(part)
	if plain != "" {
		return plain, "text"
	}
	if html != "" {
		return html, "html"
	}
	return "", "text"
}

func findBackfillTextParts(part *gmail.MessagePart) (plain, html string) {
	switch part.MimeType {
	case "text/plain":
		return decodeBackfillBody(part.Body), ""
	case "text/html":
		return "", decodeBackfillBody(part.Body)
	}
	for _, child := range part.Parts {
		p, h := findBackfillTextParts(child)
		if plain == "" {
			plain = p
		}
		if html == "" {
			html = h
		}
		if plain != "" {
			return plain, html
		}
	}
	return plain, html
}

func decodeBackfillBody(body *gmail.MessagePartBody) string {
	if body == nil || body.Data == "" {
		return ""
	}
	trimmed := strings.TrimRight(body.Data, "=")
	decoded, err := base64.RawURLEncoding.DecodeString(trimmed)
	if err != nil {
		return ""
	}
	return string(decoded)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go build ./... && go test ./... -v` (from `cli/`)
Expected: builds clean, all tests pass.

- [ ] **Step 6: Wire the `school-backfill` command into `cli/args.go` and `cli/main.go`**

In `cli/args.go`'s `parsedArgs` struct, add:
```go
SchoolBackfillSince  string
SchoolBackfillDryRun bool
```

In `parseArgs`'s flag-parsing loop, add cases (alongside the existing `--limit` case):
```go
case !endOfFlags && a == "--since":
    if i+1 >= len(args) {
        return parsedArgs{}, fmt.Errorf("--since requires a value")
    }
    i++
    res.SchoolBackfillSince = args[i]
case !endOfFlags && a == "--dry-run":
    res.SchoolBackfillDryRun = true
```

After the existing `if len(positional) > 0 && positional[0] == "discord-history"` block, add:
```go
if len(positional) > 0 && positional[0] == "school-backfill" {
    if res.SchoolBackfillSince == "" {
        return parsedArgs{}, fmt.Errorf("usage: soulman school-backfill --since YYYY-MM-DD [--dry-run]")
    }
    res.Mode = "school-backfill"
    return res, nil
}
```

In `cli/main.go`, after the existing `if args.Mode == "inject"` block, add:
```go
if args.Mode == "school-backfill" {
    if err := runSchoolBackfill(baseURL, args.SchoolBackfillSince, args.SchoolBackfillDryRun); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
    return
}
```

- [ ] **Step 7: Run the full cli suite**

Run: `go build ./... && go test ./... -v` (from `cli/`)
Expected: builds clean, all tests pass (including existing `args_test.go` tests, unaffected).

- [ ] **Step 8: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add cli/schoolbackfill.go cli/schoolbackfill_test.go cli/args.go cli/main.go cli/go.mod cli/go.sum
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "feat(cli): add school-backfill subcommand"
```

---

### Task 11: Docs — CLAUDE.md and NOTES.md updates

**Files:**
- Modify: `CLAUDE.md`
- Modify: `action-svc/NOTES.md`
- Modify: `thinking-svc/NOTES.md`
- Modify: `cli/NOTES.md` (create the "## " section pattern each existing NOTES.md already uses if a `cli/NOTES.md` doesn't yet exist — check first; if it doesn't exist, skip creating one now unless other NOTES.md files reference it as expected practice)

**Interfaces:** None — documentation only, no code interfaces.

- [ ] **Step 1: Update `CLAUDE.md`**

In the `thinking-svc` bullet's prose (point 3 under "Services"), add a sentence noting the new rule: `` `school_email` (new, 2026-09-03) → matches `@reykjavik.is` senders ahead of the generic gmail rule (prod-only via `school.enabled`), extracts actionable dates/times via a dedicated DeepSeek prompt resolved against the email's own received date. ``

In the `action-svc` bullet's prose (point 4), add a sentence: dispatches `process_school_event` (report entry always; future-dated events queued to a local `schoolevents` store), and runs a `SchoolEventScheduler` (daily tick + startup catch-up, prod-only) that sends a Discord reminder to the owner and a Google Calendar invite to a configured second recipient the day before each event — this is the one Discord path in the service that is **not** feign-gated.

Add to the Specs line for both `thinking-svc` and `action-svc`: `` `2026-09-03-school-email-events-design.md` ``.

Add a bullet to `cli`'s description: `` `soulman school-backfill --since YYYY-MM-DD [--dry-run]` (one-off historical scan for the school-email-events feature). ``

- [ ] **Step 2: Update `action-svc/NOTES.md`**

Add a new section (following the file's existing pattern — read the file first to match heading style) documenting: the `schoolevents` store's per-channel independent status tracking and why (avoids duplicate Discord pings on partial-failure retry); that this feature's Discord notifier deliberately bypasses `feign.Gate` and DND (the one exception in the service); the 2-day stale cutoff; and the manual Calendar OAuth setup requirement (`CALENDAR_CLIENT_ID`/`CALENDAR_CLIENT_SECRET`/`CALENDAR_REFRESH_TOKEN`, non-fatal if blank).

- [ ] **Step 3: Update `thinking-svc/NOTES.md`**

Add a new section documenting: `SchoolEmailRule`'s prod-only gating mechanism (`main.go` conditionally prepending to `rules.Registry`, since the registry itself has no built-in per-environment concept); that the extraction prompt resolves relative dates against the email's own `OccurredAt`, not real "now" (this is what makes backfill correct — call this out explicitly since it's easy to get backwards).

- [ ] **Step 4: Commit**

```bash
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" add CLAUDE.md action-svc/NOTES.md thinking-svc/NOTES.md cli/NOTES.md
git -C "C:\Users\Lenovo\Documents\obsidian\soulman" commit -m "docs: document the school email events feature"
```
