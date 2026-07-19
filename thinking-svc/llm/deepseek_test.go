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
