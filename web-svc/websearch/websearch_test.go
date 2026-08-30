package websearch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"soulman/web-svc/websearch"
)

func TestClient_Search_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/res/v1/web/search" {
			t.Errorf("path = %s, want /res/v1/web/search", r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "soulman ai agent" {
			t.Errorf("q = %q, want %q", got, "soulman ai agent")
		}
		if got := r.URL.Query().Get("count"); got != "10" {
			t.Errorf("count = %q, want %q", got, "10")
		}
		if got := r.Header.Get("X-Subscription-Token"); got != "test-key" {
			t.Errorf("X-Subscription-Token = %q, want test-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"web":{"results":[
			{"title":"Soulman","url":"https://example.com/soulman","description":"A <strong>personal</strong> AI agent."}
		]}}`))
	}))
	defer srv.Close()

	client := websearch.NewClient("test-key", srv.URL)
	results, err := client.Search(context.Background(), "soulman ai agent")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	want := websearch.Result{Title: "Soulman", URL: "https://example.com/soulman", Snippet: "A personal AI agent."}
	if results[0] != want {
		t.Errorf("results[0] = %+v, want %+v", results[0], want)
	}
}

func TestClient_Search_EmptyAPIKey_ReturnsErrNoAPIKeyWithoutHTTPCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer srv.Close()

	client := websearch.NewClient("", srv.URL)
	_, err := client.Search(context.Background(), "query")
	if err != websearch.ErrNoAPIKey {
		t.Fatalf("err = %v, want ErrNoAPIKey", err)
	}
	if called {
		t.Error("expected no HTTP call when apiKey is empty")
	}
}

func TestClient_Search_NonOKStatus_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()

	client := websearch.NewClient("test-key", srv.URL)
	_, err := client.Search(context.Background(), "query")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestClient_Search_Timeout_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	client := websearch.NewClient("test-key", srv.URL)
	_, err := client.Search(ctx, "query")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClient_Search_TruncatesToTenResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		results := ""
		for i := 0; i < 15; i++ {
			if i > 0 {
				results += ","
			}
			results += `{"title":"r","url":"https://example.com","description":"d"}`
		}
		w.Write([]byte(`{"web":{"results":[` + results + `]}}`))
	}))
	defer srv.Close()

	client := websearch.NewClient("test-key", srv.URL)
	results, err := client.Search(context.Background(), "query")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 10 {
		t.Errorf("len(results) = %d, want 10", len(results))
	}
}

func TestClient_Search_EmptyResults_ReturnsEmptySlice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer srv.Close()

	client := websearch.NewClient("test-key", srv.URL)
	results, err := client.Search(context.Background(), "query")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("len(results) = %d, want 0", len(results))
	}
}
