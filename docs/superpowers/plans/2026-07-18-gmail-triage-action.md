# Gmail Triage & Notify Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `thinking-svc` an LLM-judged importance rule for `gmail`-channel stimuli, and give `action-svc` a dispatch handler that always logs the email to the daily report and, when judged important, sends a debounced/batched Discord notification.

**Architecture:** `thinking-svc`'s new `GmailTriageRule` calls a new `llm.Classifier` (composed with the existing `Summarizer` into `llm.Client`, both implemented by `*DeepSeekClient`) to get an importance verdict, then always emits one `triage_gmail_email` Action Request. `action-svc`'s new dispatch handler always appends a report entry and, when the verdict is important, adds the email to a new `notifybatch.Batcher` that debounces (30s grace / 2min max-wait) before sending one combined Discord message via the existing `Notifier`.

**Tech Stack:** Go, existing `soulman/common` wire types, DeepSeek Chat Completions API, Discord Bot REST API, NATS core (unchanged, no wiring changes needed there).

## Global Constraints

- No new environment variables — reuse `thinking-svc`'s existing `DEEPSEEK_*` config and `action-svc`'s existing `DISCORD_BOT_TOKEN`/`DISCORD_CHANNEL_ID`/`REPORT_NOTIFIER` config.
- Pure LLM judgment for importance — no seeded criteria/examples in the classifier prompt.
- Classifier failure is fail-closed: `important=false`, reason records the failure; the report entry is still always written regardless of the verdict or any failure.
- Batching: 30s grace timer (resets on each new arrival) / 2min max-wait cap (measured from the first item in the batch, never reset) — both hardcoded constants (`notifybatch.DefaultGrace`, `notifybatch.DefaultMaxWait`), not environment-configurable in v1.
- The notification batcher's queue is in-memory only — accepted v1 limitation, no persistence across `action-svc` restarts.
- Do not touch `perception-svc/**`, `common/sharedconfig/**`, `config/dev.json`, `config/prod.json` — owned by the parallel Gmail-perception-channel work sharing this worktree/branch.
- `git add` only the specific files being committed in each task (never `-A` or `.`) — this worktree/branch is shared with the parallel perception-svc agent; never run destructive git commands.
- Reference spec: `docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md`.

---

### Task 1: `llm.Classifier` interface + `DeepSeekClient.ClassifyImportance`

**Files:**
- Create: `thinking-svc/llm/classifier.go`
- Modify: `thinking-svc/llm/deepseek.go`
- Test: `thinking-svc/llm/deepseek_test.go`

**Interfaces:**
- Produces: `llm.Classifier` interface (`ClassifyImportance(ctx context.Context, sender, subject, body string) (important bool, reason string, err error)`), `llm.Client` interface (`Summarizer` + `Classifier`), `(*DeepSeekClient).ClassifyImportance` — this method **never returns a non-nil error**; failures are converted into `(false, "classification unavailable: ...", nil)`.

- [ ] **Step 1: Write the failing tests**

Append to `thinking-svc/llm/deepseek_test.go` (same file, same `llm_test` package, reuses the `httptest`/`llm.NewDeepSeekClient` patterns already in the file):

```go
func TestDeepSeekClient_ClassifyImportance_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"important\":true,\"reason\":\"invoice payment overdue\"}"}}]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	important, reason, err := client.ClassifyImportance(context.Background(), "billing@example.com", "Invoice overdue", "Your invoice is overdue, please pay immediately.")
	if err != nil {
		t.Fatalf("ClassifyImportance: %v", err)
	}
	if !important {
		t.Error("important = false, want true")
	}
	if reason != "invoice payment overdue" {
		t.Errorf("reason = %q, want %q", reason, "invoice payment overdue")
	}
}

func TestDeepSeekClient_ClassifyImportance_NonOKStatus_FailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	important, reason, err := client.ClassifyImportance(context.Background(), "a@b.com", "subject", "body")
	if err != nil {
		t.Fatalf("ClassifyImportance must never return an error, got: %v", err)
	}
	if important {
		t.Error("important = true, want false (fail-closed) on non-200 status")
	}
	if !strings.Contains(reason, "classification unavailable") {
		t.Errorf("reason = %q, want it to mention classification unavailable", reason)
	}
}

func TestDeepSeekClient_ClassifyImportance_Timeout_FailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"important\":true,\"reason\":\"too late\"}"}}]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 50*time.Millisecond)
	important, reason, err := client.ClassifyImportance(context.Background(), "a@b.com", "subject", "body")
	if err != nil {
		t.Fatalf("ClassifyImportance must never return an error, got: %v", err)
	}
	if important {
		t.Error("important = true, want false (fail-closed) on timeout")
	}
	if !strings.Contains(reason, "classification unavailable") {
		t.Errorf("reason = %q, want it to mention classification unavailable", reason)
	}
}

func TestDeepSeekClient_ClassifyImportance_MalformedJSON_FailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"not json at all"}}]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	important, reason, err := client.ClassifyImportance(context.Background(), "a@b.com", "subject", "body")
	if err != nil {
		t.Fatalf("ClassifyImportance must never return an error, got: %v", err)
	}
	if important {
		t.Error("important = true, want false (fail-closed) on malformed classifier response")
	}
	if !strings.Contains(reason, "classification unavailable") {
		t.Errorf("reason = %q, want it to mention classification unavailable", reason)
	}
}
```

(`strings`, `context`, `net/http`, `net/http/httptest`, `time` are all already imported at the top of `deepseek_test.go` — no import changes needed there.)

- [ ] **Step 2: Run tests to verify they fail**

Run (from `thinking-svc/`): `go test ./llm/... -run ClassifyImportance -v`
Expected: FAIL — `client.ClassifyImportance undefined (type *llm.DeepSeekClient has no field or method ClassifyImportance)`

- [ ] **Step 3: Create `thinking-svc/llm/classifier.go`**

```go
package llm

import "context"

// Classifier judges whether an email is important enough that the user
// should look at it as soon as possible, based on its sender, subject, and
// body. *DeepSeekClient is the production implementation (see
// deepseek.go); tests inject a fake to exercise
// thinking-svc/rules.GmailTriageRule without a network call or a real
// DEEPSEEK_API_KEY.
type Classifier interface {
	ClassifyImportance(ctx context.Context, sender, subject, body string) (important bool, reason string, err error)
}

// Client composes both LLM capabilities thinking-svc's rules currently
// need. Rule.Handle takes a single Client rather than one parameter per
// capability, so a future rule needing a new LLM capability grows this
// interface instead of Rule.Handle's parameter list.
type Client interface {
	Summarizer
	Classifier
}

// classifierSystemPrompt is deliberately a plain string constant — the
// single easiest place to tweak how importance is judged. v1 uses pure LLM
// judgment with no seeded criteria/examples (see
// docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md);
// expect to hand-tune this prompt once real false positives/negatives are
// observed — that correction feedback loop itself is out of scope for now.
const classifierSystemPrompt = `You judge whether an email is important enough that the user should look at it as soon as possible, based on its sender, subject, and body. Respond with strict JSON only, no markdown and no extra text, in exactly this shape: {"important": true or false, "reason": "<one-sentence reason, under 140 characters>"}.`
```

- [ ] **Step 4: Add `ClassifyImportance` to `thinking-svc/llm/deepseek.go`**

Add this at the end of the file (after the existing `Summarize` method), reusing the existing `chatMessage`/`chatRequest`/`chatResponse` structs already defined in this file:

```go
// classifyResponse is the expected shape of the classifier's JSON response
// content (parsed from chatResponse.Choices[0].Message.Content).
type classifyResponse struct {
	Important bool   `json:"important"`
	Reason    string `json:"reason"`
}

// ClassifyImportance sends a single non-streaming Chat Completions request
// asking whether an email is important. Unlike Summarize, this method
// never returns a non-nil error: any failure (network, non-200, malformed
// response) is converted into (false, "classification unavailable: ...")
// instead — a fail-closed default so an LLM hiccup never triggers a
// spurious Discord notification, while the caller (GmailTriageRule) still
// gets a reason string worth logging to the daily report.
func (c *DeepSeekClient) ClassifyImportance(ctx context.Context, sender, subject, body string) (bool, string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	userMsg := fmt.Sprintf("From: %s\nSubject: %s\n\n%s", sender, subject, body)

	reqBody, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: classifierSystemPrompt},
			{Role: "user", Content: userMsg},
		},
		Stream: false,
	})
	if err != nil {
		return false, fmt.Sprintf("classification unavailable: marshal request: %v", err), nil
	}

	url := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return false, fmt.Sprintf("classification unavailable: build request: %v", err), nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return false, fmt.Sprintf("classification unavailable: request failed: %v", err), nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Sprintf("classification unavailable: read response: %v", err), nil
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("classification unavailable: deepseek status %d", resp.StatusCode), nil
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil || len(parsed.Choices) == 0 {
		return false, "classification unavailable: empty or malformed deepseek response", nil
	}

	var result classifyResponse
	if err := json.Unmarshal([]byte(parsed.Choices[0].Message.Content), &result); err != nil {
		return false, "classification unavailable: non-JSON classifier response", nil
	}

	return result.Important, result.Reason, nil
}
```

No new imports needed in `deepseek.go` — `bytes`, `context`, `encoding/json`, `fmt`, `io`, `net/http`, `time` are all already imported.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./llm/... -v`
Expected: PASS — all `TestDeepSeekClient_*` tests, including the 4 new ones and the pre-existing `Summarize` tests.

- [ ] **Step 6: Commit**

```bash
git add thinking-svc/llm/classifier.go thinking-svc/llm/deepseek.go thinking-svc/llm/deepseek_test.go
git commit -m "feat(thinking-svc): add llm.Classifier and DeepSeekClient.ClassifyImportance"
```

---

### Task 2: Generalize `Rule.Handle`/`Process` from `llm.Summarizer` to `llm.Client`

**Files:**
- Modify: `thinking-svc/rules/rule.go`
- Modify: `thinking-svc/rules/error_report.go`
- Modify: `thinking-svc/rules/rule_test.go`

**Interfaces:**
- Consumes: `llm.Client` from Task 1.
- Produces: `Rule.Handle func(ctx context.Context, s *common.Stimulus, client llm.Client) (*common.ActionRequest, error)`; `Process(ctx context.Context, s *common.Stimulus, client llm.Client) (*common.ActionRequest, error)`; an extended `fakeSummarizer` (in `rule_test.go`) that now also implements `llm.Client` via a configurable `ClassifyImportance`, reusable by Task 3's new test file.

- [ ] **Step 1: Extend `rule_test.go`'s existing `fakeSummarizer` to also satisfy `llm.Client`**

In `thinking-svc/rules/rule_test.go`, replace:

```go
type fakeSummarizer struct {
	summary string
	err     error
}

func (f *fakeSummarizer) Summarize(_ context.Context, _ string) (string, error) {
	return f.summary, f.err
}
```

with:

```go
type fakeSummarizer struct {
	summary string
	err     error

	classifyImportant bool
	classifyReason    string
	classifyErr       error
}

func (f *fakeSummarizer) Summarize(_ context.Context, _ string) (string, error) {
	return f.summary, f.err
}

func (f *fakeSummarizer) ClassifyImportance(_ context.Context, _, _, _ string) (bool, string, error) {
	return f.classifyImportant, f.classifyReason, f.classifyErr
}
```

This is additive — every existing use of `&fakeSummarizer{summary: "..."}` in `rule_test.go` and `error_report_test.go` keeps compiling unchanged (the new fields default to their zero values, and `ErrorReportRule` never calls `ClassifyImportance`).

- [ ] **Step 2: Run the existing test suite to verify it still fails to compile (proves the type is currently `llm.Summarizer`, not yet `llm.Client`)**

Run (from `thinking-svc/`): `go build ./...`
Expected: this actually still **succeeds** at this point — `fakeSummarizer` satisfying a superset interface doesn't break anything yet. This step is a checkpoint, not a red/green TDD gate; proceed to Step 3.

- [ ] **Step 3: Change `Rule.Handle`'s and `Process`'s signature in `thinking-svc/rules/rule.go`**

Replace:

```go
type Rule struct {
	Name   string
	Match  func(*common.Stimulus) bool
	Handle func(ctx context.Context, s *common.Stimulus, summarizer llm.Summarizer) (*common.ActionRequest, error)
}
```

with:

```go
type Rule struct {
	Name   string
	Match  func(*common.Stimulus) bool
	Handle func(ctx context.Context, s *common.Stimulus, client llm.Client) (*common.ActionRequest, error)
}
```

Replace:

```go
func Process(ctx context.Context, s *common.Stimulus, summarizer llm.Summarizer) (*common.ActionRequest, error) {
	r := Match(s)
	if r == nil {
		return nil, nil
	}
	return r.Handle(ctx, s, summarizer)
}
```

with:

```go
func Process(ctx context.Context, s *common.Stimulus, client llm.Client) (*common.ActionRequest, error) {
	r := Match(s)
	if r == nil {
		return nil, nil
	}
	return r.Handle(ctx, s, client)
}
```

- [ ] **Step 4: Update `error_report.go`'s signature (type only, no behavior change)**

In `thinking-svc/rules/error_report.go`, replace:

```go
func handleErrorReport(_ context.Context, s *common.Stimulus, _ llm.Summarizer) (*common.ActionRequest, error) {
```

with:

```go
func handleErrorReport(_ context.Context, s *common.Stimulus, _ llm.Client) (*common.ActionRequest, error) {
```

Also update the doc comment directly above it — replace "The summarizer parameter stays unused here, threaded through only because Rule.Handle's signature is shared with future rules (classification, evaluation, etc.) that will need one." with "The client parameter stays unused here, threaded through only because Rule.Handle's signature is shared with other rules (e.g. GmailTriageRule) that need Classify/Summarize capabilities this rule doesn't."

- [ ] **Step 5: Run the full `rules` and `llm` test suites to verify everything still passes**

Run: `go build ./... && go test ./... -v`
Expected: PASS — all pre-existing tests in `rules` and `llm` packages, unchanged behavior.

- [ ] **Step 6: Commit**

```bash
git add thinking-svc/rules/rule.go thinking-svc/rules/error_report.go thinking-svc/rules/rule_test.go
git commit -m "refactor(thinking-svc): generalize Rule.Handle from llm.Summarizer to llm.Client"
```

---

### Task 3: `GmailTriageRule`

**Files:**
- Create: `thinking-svc/rules/gmail_triage.go`
- Create: `thinking-svc/rules/gmail_triage_test.go`
- Modify: `thinking-svc/rules/rule.go` (register in `Registry`)
- Modify: `thinking-svc/rules/rule_test.go` (add one `Match` test)

**Interfaces:**
- Consumes: `llm.Client` (Task 1), `Rule`/`Registry`/`Process` (Task 2), `common.Stimulus` (existing), `common.ActionRequest` (existing).
- Produces: `rules.GmailTriageRule` (a `Rule` value, `Name: "gmail-triage"`), registered in `rules.Registry`. Action Request `action_hint: "triage_gmail_email"`, `Parameters` JSON shape: `{"sender","subject","body_excerpt","reason","important","thread_id","occurred_at"}` — this is the exact shape Task 6 (`action-svc/dispatch/gmail_triage.go`) must unmarshal.

- [ ] **Step 1: Write the failing tests in `thinking-svc/rules/gmail_triage_test.go`**

```go
package rules_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"soulman/common"
	"soulman/thinking-svc/rules"
)

var errClassifyBoom = errors.New("boom")

func newGmailStimulus(sender, subject, body string, occurredAt time.Time) *common.Stimulus {
	channelSpecific, _ := json.Marshal(map[string]any{"subject": subject, "label_ids": []string{"UNREAD"}})
	return &common.Stimulus{
		StimulusID: "stim-gmail-001",
		Channel:    "gmail",
		ReceivedAt: time.Now().UTC(),
		OccurredAt: &occurredAt,
		Source:     common.Source{Identity: sender},
		Content: common.Content{
			RawText:     body,
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
		},
		ChannelMeta: common.ChannelMeta{
			MessageID:       "msg-001",
			ThreadID:        "thread-001",
			ChannelSpecific: channelSpecific,
		},
		Hints:    common.Hints{Priority: "normal", Tags: []string{"email", "gmail"}},
		Override: common.Override{Params: json.RawMessage(`{}`)},
	}
}

func TestGmailTriageRule_Match_GmailChannel(t *testing.T) {
	s := newGmailStimulus("a@b.com", "subject", "body", time.Now())
	if !rules.GmailTriageRule.Match(s) {
		t.Error("expected match for gmail channel")
	}
}

func TestGmailTriageRule_Match_OtherChannel(t *testing.T) {
	s := newGmailStimulus("a@b.com", "subject", "body", time.Now())
	s.Channel = "folder-watcher"
	if rules.GmailTriageRule.Match(s) {
		t.Error("expected no match for non-gmail channel")
	}
}

func TestGmailTriageRule_Handle_Important_BuildsHighUrgencyRequest(t *testing.T) {
	occurred := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	s := newGmailStimulus("boss@company.com", "Urgent: server down", "Production is down, please call me.", occurred)

	client := &fakeSummarizer{classifyImportant: true, classifyReason: "production outage"}
	req, err := rules.GmailTriageRule.Handle(context.Background(), s, client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if req.ActionHint != "triage_gmail_email" {
		t.Errorf("ActionHint = %q, want triage_gmail_email", req.ActionHint)
	}
	if req.Urgency != "high" {
		t.Errorf("Urgency = %q, want high", req.Urgency)
	}
	if req.Intent != "Notify me about this important email" {
		t.Errorf("Intent = %q, want the important-path intent text", req.Intent)
	}
	if req.RiskLevel != "low" {
		t.Errorf("RiskLevel = %q, want low", req.RiskLevel)
	}
	if req.CorrelationID == "" {
		t.Error("CorrelationID must be generated")
	}

	var params map[string]any
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	if params["important"] != true {
		t.Errorf("important = %v, want true", params["important"])
	}
	if params["reason"] != "production outage" {
		t.Errorf("reason = %v, want %q", params["reason"], "production outage")
	}
	if params["sender"] != "boss@company.com" {
		t.Errorf("sender = %v, want boss@company.com", params["sender"])
	}
	if params["subject"] != "Urgent: server down" {
		t.Errorf("subject = %v, want %q", params["subject"], "Urgent: server down")
	}
	if params["thread_id"] != "thread-001" {
		t.Errorf("thread_id = %v, want thread-001", params["thread_id"])
	}
}

func TestGmailTriageRule_Handle_NotImportant_BuildsNormalUrgencyRequest(t *testing.T) {
	s := newGmailStimulus("newsletter@example.com", "Weekly digest", "Here's what happened this week...", time.Now())

	client := &fakeSummarizer{classifyImportant: false, classifyReason: "routine newsletter"}
	req, err := rules.GmailTriageRule.Handle(context.Background(), s, client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if req.Urgency != "normal" {
		t.Errorf("Urgency = %q, want normal", req.Urgency)
	}
	if req.Intent != "Log this email to today's daily report" {
		t.Errorf("Intent = %q, want the not-important-path intent text", req.Intent)
	}

	var params map[string]any
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	if params["important"] != false {
		t.Errorf("important = %v, want false", params["important"])
	}
}

func TestGmailTriageRule_Handle_ClassifierError_FailsClosed(t *testing.T) {
	s := newGmailStimulus("a@b.com", "subject", "body", time.Now())

	client := &fakeSummarizer{classifyErr: errClassifyBoom}
	req, err := rules.GmailTriageRule.Handle(context.Background(), s, client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var params map[string]any
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	if params["important"] != false {
		t.Errorf("important = %v, want false when classifier errors", params["important"])
	}
	reason, _ := params["reason"].(string)
	if reason == "" {
		t.Error("expected a non-empty reason describing the classifier failure")
	}
}

func TestGmailTriageRule_Handle_BodyExcerptTruncatedTo200Chars(t *testing.T) {
	longBody := make([]rune, 500)
	for i := range longBody {
		longBody[i] = 'x'
	}
	s := newGmailStimulus("a@b.com", "subject", string(longBody), time.Now())

	client := &fakeSummarizer{classifyImportant: false, classifyReason: "not important"}
	req, err := rules.GmailTriageRule.Handle(context.Background(), s, client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var params map[string]any
	if err := json.Unmarshal(req.Parameters, &params); err != nil {
		t.Fatalf("decode Parameters: %v", err)
	}
	excerpt, _ := params["body_excerpt"].(string)
	if len([]rune(excerpt)) != 201 { // 200 chars + the truncation ellipsis
		t.Errorf("excerpt rune length = %d, want 201 (200 + ellipsis)", len([]rune(excerpt)))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from `thinking-svc/`): `go test ./rules/... -run GmailTriage -v`
Expected: FAIL — `undefined: rules.GmailTriageRule`

- [ ] **Step 3: Create `thinking-svc/rules/gmail_triage.go`**

```go
package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"soulman/common"
	"soulman/thinking-svc/llm"
)

// gmailTriageParams mirrors gmail-triage-action-design.md's Thinking Rule
// parameters shape. Marshaled into common.ActionRequest.Parameters as raw
// JSON so action-svc can unmarshal it directly into its own params struct.
type gmailTriageParams struct {
	Sender      string     `json:"sender"`
	Subject     string     `json:"subject"`
	BodyExcerpt string     `json:"body_excerpt"`
	Reason      string     `json:"reason"`
	Important   bool       `json:"important"`
	ThreadID    string     `json:"thread_id"`
	OccurredAt  *time.Time `json:"occurred_at"`
}

// classifyBodyTruncateLen bounds cost/latency on the classification call,
// mirroring error_report's precedent of truncating summarizer input; the
// full body is never sent to action-svc anyway; only the shorter excerpt
// below travels through.
const classifyBodyTruncateLen = 4000

// excerptLen is the length of the body excerpt carried in the Action
// Request for both the report entry and the eventual Discord message.
const excerptLen = 200

// GmailTriageRule implements
// docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md: every
// gmail-channel stimulus becomes a triage_gmail_email Action Request. The
// report-entry half always happens in action-svc's dispatch handler; the
// Discord-notify half is conditional on the "important" verdict decided
// here.
var GmailTriageRule = Rule{
	Name: "gmail-triage",
	Match: func(s *common.Stimulus) bool {
		return s.Channel == "gmail"
	},
	Handle: handleGmailTriage,
}

func handleGmailTriage(ctx context.Context, s *common.Stimulus, client llm.Client) (*common.ActionRequest, error) {
	sender := s.Source.Identity
	subject := gmailSubject(s)
	body := s.Content.RawText
	threadID := s.ChannelMeta.ThreadID

	important, reason, err := client.ClassifyImportance(ctx, sender, subject, truncate(body, classifyBodyTruncateLen))
	if err != nil {
		// Belt-and-suspenders: production *DeepSeekClient never returns a
		// non-nil error (it fails closed internally — see deepseek.go), but
		// a future or fake Classifier implementation might; treat that the
		// same fail-closed way.
		important = false
		reason = fmt.Sprintf("classification unavailable: %v", err)
	}

	params, err := json.Marshal(gmailTriageParams{
		Sender:      sender,
		Subject:     subject,
		BodyExcerpt: truncate(body, excerptLen),
		Reason:      reason,
		Important:   important,
		ThreadID:    threadID,
		OccurredAt:  s.OccurredAt,
	})
	if err != nil {
		return nil, fmt.Errorf("rules: marshal gmail triage parameters: %w", err)
	}

	intent := "Log this email to today's daily report"
	urgency := "normal"
	if important {
		intent = "Notify me about this important email"
		urgency = "high"
	}

	req := &common.ActionRequest{
		CorrelationID:   uuid.NewString(),
		Intent:          intent,
		ActionHint:      "triage_gmail_email",
		Parameters:      params,
		RiskLevel:       "low",
		Urgency:         urgency,
		ExpectedOutcome: "one report entry appended, plus an immediate (debounced) Discord notification if judged important",
		Fallback:        "if report append fails, retry once; if it fails again, log to episodic memory with error:execution tag and give up silently. If the Discord notification fails, no retry is attempted — a missed immediate ping is not worth blocking on since the report entry is the permanent record.",
	}
	return req, nil
}

// gmailSubject extracts channel_metadata.channel_specific.subject, the
// field gmail-channel-design.md guarantees for gmail stimuli.
func gmailSubject(s *common.Stimulus) string {
	var meta struct {
		Subject string `json:"subject"`
	}
	if len(s.ChannelMeta.ChannelSpecific) > 0 {
		_ = json.Unmarshal(s.ChannelMeta.ChannelSpecific, &meta)
	}
	return meta.Subject
}

// truncate returns s cut to at most n runes, appending "…" when truncation
// actually occurred. Operates on runes (not bytes) so multi-byte UTF-8
// characters in a sender name or body are never split mid-character.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./rules/... -run GmailTriage -v`
Expected: PASS — all `TestGmailTriageRule_*` tests.

- [ ] **Step 5: Register `GmailTriageRule` in `thinking-svc/rules/rule.go`'s `Registry`**

Replace:

```go
var Registry = []Rule{
	ErrorReportRule,
}
```

with:

```go
var Registry = []Rule{
	ErrorReportRule,
	GmailTriageRule,
}
```

- [ ] **Step 6: Add a `Match` test through the shared registry in `thinking-svc/rules/rule_test.go`**

Append:

```go
func TestMatch_FindsGmailTriageRule(t *testing.T) {
	s := newGmailStimulus("a@b.com", "subject", "body", time.Now())
	r := rules.Match(s)
	if r == nil {
		t.Fatal("Match = nil, want GmailTriageRule for gmail stimulus")
	}
	if r.Name != "gmail-triage" {
		t.Errorf("Name = %q, want gmail-triage", r.Name)
	}
}
```

- [ ] **Step 7: Run the full test suite to verify everything passes**

Run: `go build ./... && go test ./... -v`
Expected: PASS — all tests across `thinking-svc`.

- [ ] **Step 8: Commit**

```bash
git add thinking-svc/rules/gmail_triage.go thinking-svc/rules/gmail_triage_test.go thinking-svc/rules/rule.go thinking-svc/rules/rule_test.go
git commit -m "feat(thinking-svc): add GmailTriageRule for gmail-channel stimuli"
```

---

### Task 4: Wire `GmailTriageRule`'s dependency (`llm.Client`) into `thinking-svc/main.go`

**Files:**
- Modify: `thinking-svc/main.go`

**Interfaces:**
- Consumes: `llm.Client` (Task 1), `rules.Process` (Task 2, now taking `llm.Client`).
- Produces: no new exported symbols — this is a wiring-only task, verified by build success (no unit tests target `main.go`, matching the existing convention in this codebase).

- [ ] **Step 1: Rename `stimulusHandler`'s field and update `rules.Process`'s call site**

In `thinking-svc/main.go`, replace:

```go
type stimulusHandler struct {
	summarizer llm.Summarizer
	publisher  *natsclient.Publisher
}

func (h *stimulusHandler) Handle(ctx context.Context, s *common.Stimulus) error {
	req, err := rules.Process(ctx, s, h.summarizer)
```

with:

```go
type stimulusHandler struct {
	client    llm.Client
	publisher *natsclient.Publisher
}

func (h *stimulusHandler) Handle(ctx context.Context, s *common.Stimulus) error {
	req, err := rules.Process(ctx, s, h.client)
```

And replace:

```go
	handler := &stimulusHandler{summarizer: summarizer, publisher: publisher}
```

with:

```go
	handler := &stimulusHandler{client: summarizer, publisher: publisher}
```

(The local variable is still named `summarizer` — it's still constructed via `llm.NewDeepSeekClient(...)` a few lines above, unchanged; only the struct field it's assigned to changes type/name, since `*DeepSeekClient` now satisfies `llm.Client` automatically after Task 1.)

- [ ] **Step 2: Verify the build succeeds**

Run (from `thinking-svc/`): `go build ./...`
Expected: builds cleanly with no errors.

- [ ] **Step 3: Run the full test suite one more time as a regression check**

Run: `go test ./... -v`
Expected: PASS — all tests across `thinking-svc` (this task touches no test-covered logic, only `main.go` wiring).

- [ ] **Step 4: Commit**

```bash
git add thinking-svc/main.go
git commit -m "feat(thinking-svc): wire GmailTriageRule's llm.Client dependency into main"
```

---

### Task 5: `notifybatch.Batcher` (debounce with max-wait)

**Files:**
- Create: `action-svc/notifybatch/batcher.go`
- Create: `action-svc/notifybatch/batcher_test.go`

**Interfaces:**
- Consumes: `notify.Notifier` (existing, `action-svc/notify/notifier.go`).
- Produces: `notifybatch.Item{Sender, Subject, Reason, BodyExcerpt, ThreadID string}`; `notifybatch.New(grace, maxWait time.Duration, notifier notify.Notifier) *Batcher`; `(*Batcher).Add(item Item)`; `(*Batcher).Flush()`; exported constants `notifybatch.DefaultGrace = 30 * time.Second`, `notifybatch.DefaultMaxWait = 2 * time.Minute` — Task 6 and Task 7 both depend on these exact names.

- [ ] **Step 1: Write the failing tests in `action-svc/notifybatch/batcher_test.go`**

```go
package notifybatch_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"soulman/action-svc/notifybatch"
)

type fakeNotifier struct {
	mu       sync.Mutex
	messages []string
	sendCh   chan string
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{sendCh: make(chan string, 10)}
}

func (f *fakeNotifier) Send(message string) error {
	f.mu.Lock()
	f.messages = append(f.messages, message)
	f.mu.Unlock()
	f.sendCh <- message
	return nil
}

func (f *fakeNotifier) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.messages...)
}

func waitForSend(t *testing.T, f *fakeNotifier, timeout time.Duration) string {
	t.Helper()
	select {
	case msg := <-f.sendCh:
		return msg
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a Send call")
		return ""
	}
}

func TestBatcher_SingleItem_FlushesAfterGracePeriod(t *testing.T) {
	notifier := newFakeNotifier()
	b := notifybatch.New(40*time.Millisecond, 2*time.Second, notifier)

	b.Add(notifybatch.Item{Sender: "a@b.com", Subject: "hi", Reason: "r", BodyExcerpt: "excerpt", ThreadID: "t1"})

	msg := waitForSend(t, notifier, time.Second)
	if !strings.Contains(msg, "1 important email(s):") {
		t.Errorf("message = %q, want a 1-item header", msg)
	}
	if !strings.Contains(msg, "a@b.com") {
		t.Errorf("message = %q, want it to contain the sender", msg)
	}

	time.Sleep(100 * time.Millisecond)
	if len(notifier.sent()) != 1 {
		t.Errorf("Send called %d times, want exactly 1", len(notifier.sent()))
	}
}

func TestBatcher_MultipleItemsWithinGracePeriod_CombineIntoOneSend(t *testing.T) {
	notifier := newFakeNotifier()
	b := notifybatch.New(60*time.Millisecond, 2*time.Second, notifier)

	b.Add(notifybatch.Item{Sender: "a@b.com", Subject: "one", Reason: "r1", BodyExcerpt: "e1", ThreadID: "t1"})
	time.Sleep(15 * time.Millisecond)
	b.Add(notifybatch.Item{Sender: "c@d.com", Subject: "two", Reason: "r2", BodyExcerpt: "e2", ThreadID: "t2"})

	msg := waitForSend(t, notifier, time.Second)
	if !strings.Contains(msg, "2 important email(s):") {
		t.Errorf("message = %q, want a 2-item header", msg)
	}
	if !strings.Contains(msg, "one") || !strings.Contains(msg, "two") {
		t.Errorf("message = %q, want both subjects present", msg)
	}

	time.Sleep(150 * time.Millisecond)
	if len(notifier.sent()) != 1 {
		t.Errorf("Send called %d times, want exactly 1 combined send", len(notifier.sent()))
	}
}

func TestBatcher_SteadyTrickle_FlushesAtMaxWaitCap(t *testing.T) {
	notifier := newFakeNotifier()
	maxWait := 150 * time.Millisecond
	b := notifybatch.New(50*time.Millisecond, maxWait, notifier)

	start := time.Now()
	stop := time.After(300 * time.Millisecond)
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()

loop:
	for {
		select {
		case <-ticker.C:
			b.Add(notifybatch.Item{Sender: "a@b.com", Subject: "trickle", Reason: "r", BodyExcerpt: "e", ThreadID: "t"})
		case <-stop:
			break loop
		}
	}

	msg := waitForSend(t, notifier, time.Second)
	elapsed := time.Since(start)
	if elapsed > 250*time.Millisecond {
		t.Errorf("first flush took %v, want it forced by the %v max-wait cap well before the 300ms trickle stopped", elapsed, maxWait)
	}
	if !strings.Contains(msg, "important email(s):") {
		t.Errorf("message = %q, want the batch header", msg)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from `action-svc/`): `go test ./notifybatch/... -v`
Expected: FAIL — `no required module provides package soulman/action-svc/notifybatch` (package doesn't exist yet).

- [ ] **Step 3: Create `action-svc/notifybatch/batcher.go`**

```go
package notifybatch

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"soulman/action-svc/notify"
)

// DefaultGrace and DefaultMaxWait are the hardcoded (not environment-
// configurable, per the design spec) debounce durations action-svc's
// main.go constructs its Batcher with.
const (
	DefaultGrace   = 30 * time.Second
	DefaultMaxWait = 2 * time.Minute
)

// Item is one important-email notification queued for the next flush.
type Item struct {
	Sender      string
	Subject     string
	Reason      string
	BodyExcerpt string
	ThreadID    string
}

// Batcher collects important-email Items and flushes them as a single
// Discord message once either the grace period (no new item has arrived
// recently) or the max-wait cap (measured from the first item in the
// pending batch) elapses — whichever comes first. See
// docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md's
// "Notification batching" section for the rationale behind the two
// timers. The queue is in-memory only: a process restart with a batch
// pending loses it (an accepted v1 limitation).
type Batcher struct {
	grace    time.Duration
	maxWait  time.Duration
	notifier notify.Notifier

	mu         sync.Mutex
	items      []Item
	graceTimer *time.Timer
	maxTimer   *time.Timer
}

func New(grace, maxWait time.Duration, notifier notify.Notifier) *Batcher {
	return &Batcher{grace: grace, maxWait: maxWait, notifier: notifier}
}

// Add queues item for the next flush. The first item in a new batch starts
// both timers; later items reset only the grace timer — the max-wait
// timer keeps counting from the first item and is never reset, bounding
// worst-case delay during a steady trickle of arrivals.
func (b *Batcher) Add(item Item) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.items = append(b.items, item)

	if b.graceTimer == nil {
		b.maxTimer = time.AfterFunc(b.maxWait, b.Flush)
		b.graceTimer = time.AfterFunc(b.grace, b.Flush)
		return
	}

	b.graceTimer.Stop()
	b.graceTimer = time.AfterFunc(b.grace, b.Flush)
}

// Flush sends all currently-queued items as one message and clears the
// batch. Safe to call when the batch is already empty — a no-op — which is
// how the timer that loses the grace-vs-max-wait race resolves once it
// fires after the other timer already flushed.
func (b *Batcher) Flush() {
	b.mu.Lock()
	items := b.items
	b.items = nil
	if b.graceTimer != nil {
		b.graceTimer.Stop()
		b.graceTimer = nil
	}
	if b.maxTimer != nil {
		b.maxTimer.Stop()
		b.maxTimer = nil
	}
	b.mu.Unlock()

	if len(items) == 0 {
		return
	}
	_ = b.notifier.Send(formatBatch(items))
}

func formatBatch(items []Item) string {
	blocks := make([]string, 0, len(items)+1)
	blocks = append(blocks, fmt.Sprintf("%d important email(s):", len(items)))
	for _, it := range items {
		blocks = append(blocks, fmt.Sprintf(
			"From: %s\nSubject: %s\nWhy: %s\n\"%s\"\nhttps://mail.google.com/mail/u/0/#inbox/%s",
			it.Sender, it.Subject, it.Reason, it.BodyExcerpt, it.ThreadID))
	}
	return strings.Join(blocks, "\n\n")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./notifybatch/... -v`
Expected: PASS — all 3 `TestBatcher_*` tests. (These tests use real timers with small durations; they're deterministic in intent but timing-sensitive — if `TestBatcher_SteadyTrickle_FlushesAtMaxWaitCap` is ever flaky on a loaded machine, widen its margins rather than removing the assertion.)

- [ ] **Step 5: Commit**

```bash
git add action-svc/notifybatch/batcher.go action-svc/notifybatch/batcher_test.go
git commit -m "feat(action-svc): add notifybatch.Batcher debounce-with-max-wait notification batcher"
```

---

### Task 6: `triage_gmail_email` dispatch handler

**Files:**
- Modify: `action-svc/dispatch/dispatch.go`
- Create: `action-svc/dispatch/gmail_triage.go`
- Create: `action-svc/dispatch/gmail_triage_test.go`
- Modify: `action-svc/dispatch/dispatch_test.go` (update `dispatch.New` call sites for the new third parameter)

**Interfaces:**
- Consumes: `notifybatch.Item` (Task 5), `common.ActionRequest` (existing), `report.Entry`/`report.Append` (existing, `action-svc/report/report.go`).
- Produces: `dispatch.Batcher` interface (`Add(item notifybatch.Item)`), `dispatch.New(root string, publisher Publisher, batcher Batcher) *Dispatcher` (signature change — was `New(root string, publisher Publisher)`), `dispatch.AppendGmailReportEntry` (package-level var, same test-injection pattern as the existing `dispatch.AppendReportEntry`), `dispatch.GmailTriageParams` struct.

- [ ] **Step 1: Update `dispatch.New`'s signature in `action-svc/dispatch/dispatch.go`**

Replace:

```go
package dispatch

import (
	"encoding/json"
	"log"

	"soulman/common"
)

// Publisher is satisfied by *natsclient.Publisher. Defined here (not in
// natsclient) so this package doesn't need to import natsclient.
type Publisher interface {
	PublishOutcome(actionType, status, taskID string) error
}

type Dispatcher struct {
	root      string
	publisher Publisher
}

func New(root string, publisher Publisher) *Dispatcher {
	return &Dispatcher{root: root, publisher: publisher}
}
```

with:

```go
package dispatch

import (
	"encoding/json"
	"log"

	"soulman/action-svc/notifybatch"
	"soulman/common"
)

// Publisher is satisfied by *natsclient.Publisher. Defined here (not in
// natsclient) so this package doesn't need to import natsclient.
type Publisher interface {
	PublishOutcome(actionType, status, taskID string) error
}

// Batcher is satisfied by *notifybatch.Batcher. Defined here (not in
// notifybatch) so tests can inject a fake that records Add calls without
// waiting on real timers — flush timing itself is already covered by
// notifybatch's own tests.
type Batcher interface {
	Add(item notifybatch.Item)
}

type Dispatcher struct {
	root      string
	publisher Publisher
	batcher   Batcher
}

func New(root string, publisher Publisher, batcher Batcher) *Dispatcher {
	return &Dispatcher{root: root, publisher: publisher, batcher: batcher}
}
```

- [ ] **Step 2: Add the new case to `Handle`'s switch in the same file**

Replace:

```go
	switch req.ActionHint {
	case "append_daily_report_entry":
		d.dispatchAppendDailyReportEntry(req)
	default:
		log.Printf("dispatch: unknown action_hint %q, dropping (correlation_id=%s)", req.ActionHint, req.CorrelationID)
	}
```

with:

```go
	switch req.ActionHint {
	case "append_daily_report_entry":
		d.dispatchAppendDailyReportEntry(req)
	case "triage_gmail_email":
		d.dispatchGmailTriage(req)
	default:
		log.Printf("dispatch: unknown action_hint %q, dropping (correlation_id=%s)", req.ActionHint, req.CorrelationID)
	}
```

- [ ] **Step 3: Update the 5 existing `dispatch.New(...)` call sites in `action-svc/dispatch/dispatch_test.go`**

Every existing call is `dispatch.New(t.TempDir(), pub)` — add a third argument, `nil`, since none of those tests exercise `"triage_gmail_email"`:

```go
	d := dispatch.New(t.TempDir(), pub, nil)
```

(There are 5 occurrences: `TestHandle_UnknownActionType_DroppedWithoutPublish`, `TestHandle_AppendSuccess_PublishesSuccessOutcome`, `TestHandle_AppendFailsTwice_RetriesOnceThenPublishesFailedOutcome`, `TestHandle_BadJSON_DoesNotPanicOrPublish` — update each `dispatch.New(...)` call in this file to add `, nil` as the third argument. `TestAppendReportEntry_RealImplementation_WritesReportFile` doesn't call `dispatch.New` at all and needs no change.)

- [ ] **Step 4: Run the build to confirm the existing tests still compile (they'll still pass — this step is a checkpoint before adding new behavior)**

Run (from `action-svc/`): `go build ./... && go test ./dispatch/... -v`
Expected: PASS — all pre-existing `dispatch` tests, now compiling against the 3-argument `New`.

- [ ] **Step 5: Write the failing tests in `action-svc/dispatch/gmail_triage_test.go`**

```go
package dispatch_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"soulman/action-svc/dispatch"
	"soulman/action-svc/notifybatch"
	"soulman/common"
)

type fakeBatcher struct {
	mu    sync.Mutex
	items []notifybatch.Item
}

func (f *fakeBatcher) Add(item notifybatch.Item) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = append(f.items, item)
}

func (f *fakeBatcher) added() []notifybatch.Item {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]notifybatch.Item(nil), f.items...)
}

func gmailTriageParamsJSON(t *testing.T, sender, subject, reason string, important bool) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"sender":       sender,
		"subject":      subject,
		"body_excerpt": "excerpt text",
		"reason":       reason,
		"important":    important,
		"thread_id":    "thread-1",
		"occurred_at":  "2026-07-18T09:00:00-06:00",
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return b
}

func TestDispatch_GmailTriage_Important_AddsToBatcher(t *testing.T) {
	pub := &fakePublisher{}
	batcher := &fakeBatcher{}
	d := dispatch.New(t.TempDir(), pub, batcher)

	req := common.ActionRequest{
		CorrelationID: "g1",
		ActionHint:    "triage_gmail_email",
		Parameters:    gmailTriageParamsJSON(t, "boss@company.com", "Server down", "outage", true),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	items := batcher.added()
	if len(items) != 1 {
		t.Fatalf("batcher.Add called %d times, want 1", len(items))
	}
	if items[0].Sender != "boss@company.com" || items[0].Subject != "Server down" {
		t.Errorf("batched item = %+v, want sender/subject to match", items[0])
	}

	rec, ok := pub.last()
	if !ok || rec.status != "success" || rec.actionType != "triage_gmail_email" {
		t.Errorf("outcome = %+v, ok=%v, want status=success actionType=triage_gmail_email", rec, ok)
	}
}

func TestDispatch_GmailTriage_NotImportant_SkipsBatcher(t *testing.T) {
	pub := &fakePublisher{}
	batcher := &fakeBatcher{}
	d := dispatch.New(t.TempDir(), pub, batcher)

	req := common.ActionRequest{
		CorrelationID: "g2",
		ActionHint:    "triage_gmail_email",
		Parameters:    gmailTriageParamsJSON(t, "newsletter@example.com", "Weekly digest", "routine", false),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if items := batcher.added(); len(items) != 0 {
		t.Errorf("batcher.Add called %d times, want 0 for a not-important email", len(items))
	}
}

func TestDispatch_GmailTriage_AlwaysWritesReportEntry_RegardlessOfImportance(t *testing.T) {
	orig := dispatch.AppendGmailReportEntry
	calls := 0
	dispatch.AppendGmailReportEntry = func(root string, params json.RawMessage) (string, error) {
		calls++
		return "fake/path.txt", nil
	}
	defer func() { dispatch.AppendGmailReportEntry = orig }()

	pub := &fakePublisher{}
	batcher := &fakeBatcher{}
	d := dispatch.New(t.TempDir(), pub, batcher)

	req := common.ActionRequest{
		CorrelationID: "g3",
		ActionHint:    "triage_gmail_email",
		Parameters:    gmailTriageParamsJSON(t, "newsletter@example.com", "Weekly digest", "routine", false),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if calls != 1 {
		t.Errorf("AppendGmailReportEntry called %d times for a not-important email, want 1", calls)
	}
}

func TestDispatch_GmailTriage_ReportAppendFailsTwice_RetriesOnceThenPublishesFailedOutcome(t *testing.T) {
	orig := dispatch.AppendGmailReportEntry
	calls := 0
	dispatch.AppendGmailReportEntry = func(root string, params json.RawMessage) (string, error) {
		calls++
		return "", errors.New("boom")
	}
	defer func() { dispatch.AppendGmailReportEntry = orig }()

	pub := &fakePublisher{}
	d := dispatch.New(t.TempDir(), pub, &fakeBatcher{})

	req := common.ActionRequest{
		CorrelationID: "g4",
		ActionHint:    "triage_gmail_email",
		Parameters:    gmailTriageParamsJSON(t, "a@b.com", "s", "r", false),
	}
	b, _ := json.Marshal(req)
	d.Handle(b)

	if calls != 2 {
		t.Errorf("AppendGmailReportEntry called %d times, want 2 (one retry)", calls)
	}
	rec, ok := pub.last()
	if !ok || rec.status != "failed" {
		t.Errorf("outcome = %+v, ok=%v, want status=failed", rec, ok)
	}
}

func TestAppendGmailReportEntry_RealImplementation_WritesReportFile(t *testing.T) {
	root := t.TempDir()
	params := gmailTriageParamsJSON(t, "a@b.com", "subject", "reason text", true)

	path, err := dispatch.AppendGmailReportEntry(root, params)
	if err != nil {
		t.Fatalf("AppendGmailReportEntry: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty report path")
	}
}
```

(`fakePublisher` and `record` are already defined in `dispatch_test.go` in this same `dispatch_test` package — no need to redefine them here.)

- [ ] **Step 6: Run tests to verify they fail**

Run (from `action-svc/`): `go test ./dispatch/... -run GmailTriage -v`
Expected: FAIL — `undefined: dispatch.AppendGmailReportEntry` and `d.dispatchGmailTriage undefined` (via the missing `"triage_gmail_email"` case not yet existing — actually the switch case was added in Step 2, so the failure here is specifically the missing `AppendGmailReportEntry` var and its wiring).

- [ ] **Step 7: Create `action-svc/dispatch/gmail_triage.go`**

```go
package dispatch

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"soulman/action-svc/notifybatch"
	"soulman/action-svc/report"
	"soulman/common"
)

// GmailTriageParams mirrors thinking-svc's gmailTriageParams — the
// Parameters shape triage_gmail_email Action Requests carry.
type GmailTriageParams struct {
	Sender      string `json:"sender"`
	Subject     string `json:"subject"`
	BodyExcerpt string `json:"body_excerpt"`
	Reason      string `json:"reason"`
	Important   bool   `json:"important"`
	ThreadID    string `json:"thread_id"`
	OccurredAt  string `json:"occurred_at"`
}

// AppendGmailReportEntry implements the always-log half of
// triage_gmail_email. A package-level var (mirroring AppendReportEntry) so
// tests can inject a failing stand-in to deterministically exercise
// Dispatcher's retry-then-give-up behaviour without needing to force a
// real filesystem failure.
var AppendGmailReportEntry = func(root string, params json.RawMessage) (string, error) {
	var p GmailTriageParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("dispatch: unmarshal gmail triage params: %w", err)
	}
	occurredAt, err := time.Parse(time.RFC3339, p.OccurredAt)
	if err != nil {
		return "", fmt.Errorf("dispatch: parse occurred_at %q: %w", p.OccurredAt, err)
	}

	verdict := "not important"
	if p.Important {
		verdict = "important"
	}

	entry := report.Entry{
		Summary:    fmt.Sprintf("%s — deemed %s", p.Subject, verdict),
		RawContent: fmt.Sprintf("Reason: %s\n\n%s", p.Reason, p.BodyExcerpt),
		// report.Append's formatEntry derives the bracketed report context
		// via filepath.Dir(SourcePath) — there's no real file path for an
		// email, so the sender is synthesized as a fake "directory" by
		// appending the thread ID as a placeholder "filename" segment
		// (never itself displayed). This is the same "/" trick
		// error_report.go already relies on for folder-watcher's
		// watched_path + filename.
		SourcePath: p.Sender + "/" + p.ThreadID,
		OccurredAt: occurredAt.Local(),
	}
	path, err := report.Append(root, entry)
	if err != nil {
		return "", fmt.Errorf("dispatch: append gmail report entry: %w", err)
	}
	return path, nil
}

func (d *Dispatcher) dispatchGmailTriage(req common.ActionRequest) {
	var p GmailTriageParams
	if err := json.Unmarshal(req.Parameters, &p); err != nil {
		log.Printf("dispatch: triage_gmail_email unparseable params, dropping (correlation_id=%s): %v", req.CorrelationID, err)
		return
	}

	_, err := AppendGmailReportEntry(d.root, req.Parameters)
	if err != nil {
		log.Printf("dispatch: triage_gmail_email report append failed for task %s, retrying once: %v", req.CorrelationID, err)
		_, err = AppendGmailReportEntry(d.root, req.Parameters)
	}

	status := "success"
	if err != nil {
		status = "failed"
		log.Printf("dispatch: triage_gmail_email report append failed for task %s after retry, giving up: %v", req.CorrelationID, err)
	}

	if p.Important && d.batcher != nil {
		d.batcher.Add(notifybatch.Item{
			Sender:      p.Sender,
			Subject:     p.Subject,
			Reason:      p.Reason,
			BodyExcerpt: p.BodyExcerpt,
			ThreadID:    p.ThreadID,
		})
	}

	if d.publisher == nil {
		return
	}
	if pubErr := d.publisher.PublishOutcome(req.ActionHint, status, req.CorrelationID); pubErr != nil {
		log.Printf("dispatch: outcome publish failed for task %s: %v", req.CorrelationID, pubErr)
	}
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `go test ./dispatch/... -v`
Expected: PASS — all tests in `dispatch`, including the pre-existing ones (still compiling against the updated `New` signature) and the new `TestDispatch_GmailTriage_*`/`TestAppendGmailReportEntry_*` tests.

- [ ] **Step 9: Commit**

```bash
git add action-svc/dispatch/dispatch.go action-svc/dispatch/gmail_triage.go action-svc/dispatch/gmail_triage_test.go action-svc/dispatch/dispatch_test.go
git commit -m "feat(action-svc): add triage_gmail_email dispatch handler"
```

---

### Task 7: Wire the batcher into `action-svc/main.go`

**Files:**
- Modify: `action-svc/main.go`

**Interfaces:**
- Consumes: `notifybatch.New`, `notifybatch.DefaultGrace`, `notifybatch.DefaultMaxWait` (Task 5); `dispatch.New(root, publisher, batcher)` (Task 6, 3-argument signature).
- Produces: no new exported symbols — wiring-only, verified by build success (matching this codebase's existing convention of not unit-testing `main.go`).

- [ ] **Step 1: Add the `notifybatch` import and construct the batcher**

In `action-svc/main.go`, add `"soulman/action-svc/notifybatch"` to the import block (alongside the existing `"soulman/action-svc/notify"` import), then replace:

```go
	// NATS is non-fatal at startup: the dispatch side degrades until
	// reconnect, but the HTTP server and the daily cron don't depend on it.
	var publisher *natsclient.Publisher
	nc, natsErr := natsclient.Connect(cfg.NATSURL)
	if natsErr != nil {
		log.Printf("WARNING: nats unavailable (%v) — dispatch degraded until reconnect", natsErr)
	} else {
		publisher = natsclient.NewPublisher(nc, cfg.MemoryWriteSubject)
		disp := dispatch.New(cfg.SoulmanRoot, publisher)
		sub, subErr := natsclient.Subscribe(nc, cfg.ThinkingRequestSubject, disp.Handle)
```

with:

```go
	// Batches important-email Discord notifications from the
	// triage_gmail_email dispatch handler (30s grace / 2min max-wait — see
	// docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md).
	// Reuses the same notifier the daily cron already sends through.
	batcher := notifybatch.New(notifybatch.DefaultGrace, notifybatch.DefaultMaxWait, notifier)

	// NATS is non-fatal at startup: the dispatch side degrades until
	// reconnect, but the HTTP server and the daily cron don't depend on it.
	var publisher *natsclient.Publisher
	nc, natsErr := natsclient.Connect(cfg.NATSURL)
	if natsErr != nil {
		log.Printf("WARNING: nats unavailable (%v) — dispatch degraded until reconnect", natsErr)
	} else {
		publisher = natsclient.NewPublisher(nc, cfg.MemoryWriteSubject)
		disp := dispatch.New(cfg.SoulmanRoot, publisher, batcher)
		sub, subErr := natsclient.Subscribe(nc, cfg.ThinkingRequestSubject, disp.Handle)
```

- [ ] **Step 2: Verify the build succeeds**

Run (from `action-svc/`): `go build ./...`
Expected: builds cleanly with no errors.

- [ ] **Step 3: Run the full test suite one more time as a regression check**

Run: `go test ./... -v`
Expected: PASS — all tests across `action-svc` (`dispatch`, `notifybatch`, `notify`, `report`, `scheduler`).

- [ ] **Step 4: Commit**

```bash
git add action-svc/main.go
git commit -m "feat(action-svc): wire notifybatch.Batcher into main"
```

---

## After All Tasks

Both services should build and test cleanly end-to-end:

```bash
cd thinking-svc && go build ./... && go test ./...
cd ../action-svc && go build ./... && go test ./...
```

No manual/live verification is possible without real DeepSeek/Discord credentials and a real `gmail`-channel stimulus (which depends on the parallel `perception-svc` Gmail channel work landing) — this matches the project's existing acceptance of "verified manually once live dependencies exist" for DeepSeek and Discord elsewhere in this codebase.
