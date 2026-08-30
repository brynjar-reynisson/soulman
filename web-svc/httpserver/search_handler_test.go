package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/web-svc/auth"
	"soulman/web-svc/httpserver"
)

func TestAPISearch_ReturnsResults(t *testing.T) {
	brave := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"web":{"results":[{"title":"Soulman","url":"https://example.com","description":"desc"}]}}`))
	}))
	defer brave.Close()

	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		BraveSearchAPIKey:  "test-key",
		BraveSearchBaseURL: brave.URL,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=soulman", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body.Results) != 1 || body.Results[0].Title != "Soulman" {
		t.Errorf("results = %+v", body.Results)
	}
}

func TestAPISearch_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", BraveSearchAPIKey: "test-key"}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=soulman", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAPISearch_EmptyQuery_Returns400(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", BraveSearchAPIKey: "test-key"}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPISearch_NoAPIKeyConfigured_Returns503(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=soulman", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPISearch_BraveError_Returns502(t *testing.T) {
	brave := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer brave.Close()

	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		BraveSearchAPIKey:  "test-key",
		BraveSearchBaseURL: brave.URL,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=soulman", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPISearch_EmptyResults_Returns200WithEmptyList(t *testing.T) {
	brave := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer brave.Close()

	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		BraveSearchAPIKey:  "test-key",
		BraveSearchBaseURL: brave.URL,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=soulman", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Results []struct{} `json:"results"`
	}
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body.Results) != 0 {
		t.Errorf("results = %+v, want empty", body.Results)
	}
}
