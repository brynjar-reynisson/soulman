package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"soulman/thinking-svc/llm"
)

func TestDeepSeekClient_Summarize_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}

		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "deepseek-chat" {
			t.Errorf("model = %v, want deepseek-chat", body["model"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"one line summary"}}]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	summary, err := client.Summarize(context.Background(), "some error text")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if summary != "one line summary" {
		t.Errorf("summary = %q, want %q", summary, "one line summary")
	}
}

func TestDeepSeekClient_Summarize_NonOKStatus_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	_, err := client.Summarize(context.Background(), "some error text")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestDeepSeekClient_Summarize_Timeout_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"choices":[{"message":{"content":"too late"}}]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 50*time.Millisecond)
	_, err := client.Summarize(context.Background(), "some error text")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestDeepSeekClient_Summarize_EmptyChoices_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	_, err := client.Summarize(context.Background(), "some error text")
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

// TestDeepSeekClient_LiveAPI exercises the real DeepSeek API. It requires a
// real DEEPSEEK_API_KEY and is skipped otherwise. The repo owner provides
// the key outside of this environment — it is never hardcoded here.
func TestDeepSeekClient_LiveAPI(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("DEEPSEEK_API_KEY not set — skipping live DeepSeek API test")
	}

	client := llm.NewDeepSeekClient(apiKey, "https://api.deepseek.com", "deepseek-chat", 15*time.Second)
	summary, err := client.Summarize(context.Background(), "connection timeout to remote host at 10.0.0.5:443")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if strings.TrimSpace(summary) == "" {
		t.Error("expected non-empty summary from live API")
	}
	if len(summary) > 500 {
		t.Errorf("summary unexpectedly long (%d chars): %q", len(summary), summary)
	}
}

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

func TestDeepSeekClient_ExtractSchoolEvents_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"events\":[{\"date\":\"2026-09-04\",\"has_time\":false,\"time\":\"\",\"description\":\"Sweater day\"}]}"}}]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	ref := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	events, note, err := client.ExtractSchoolEvents(context.Background(), "teacher@reykjavik.is", "Reminder", "Don't forget tomorrow is sweater day!", ref, nil)
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
	events, _, err := client.ExtractSchoolEvents(context.Background(), "info@reykjavik.is", "Notice", "Unrelated municipal notice.", time.Now(), nil)
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
	events, note, err := client.ExtractSchoolEvents(context.Background(), "a@reykjavik.is", "s", "b", time.Now(), nil)
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
	client.ExtractSchoolEvents(context.Background(), "a@reykjavik.is", "s", "b", ref, nil)

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

func TestDeepSeekClient_ExtractSchoolEvents_RelevantGradesInPrompt(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"events\":[]}"}}]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	client.ExtractSchoolEvents(context.Background(), "a@reykjavik.is", "s", "b", time.Now(), []string{"5", "8"})

	messages, _ := capturedBody["messages"].([]interface{})
	system, _ := messages[0].(map[string]interface{})
	content, _ := system["content"].(string)
	if !strings.Contains(content, "5, 8") {
		t.Errorf("system prompt = %q, want it to contain the relevant grades \"5, 8\"", content)
	}
}

func TestDeepSeekClient_ExtractSchoolEvents_NoGrades_OmitsGradeClause(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"events\":[]}"}}]}`))
	}))
	defer srv.Close()

	client := llm.NewDeepSeekClient("test-key", srv.URL, "deepseek-chat", 5*time.Second)
	client.ExtractSchoolEvents(context.Background(), "a@reykjavik.is", "s", "b", time.Now(), nil)

	messages, _ := capturedBody["messages"].([]interface{})
	system, _ := messages[0].(map[string]interface{})
	content, _ := system["content"].(string)
	if strings.Contains(content, "Only include events relevant to these grades") {
		t.Errorf("system prompt = %q, want no grade-filtering clause when relevantGrades is empty", content)
	}
}
