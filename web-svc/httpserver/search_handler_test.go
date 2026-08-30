package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestAPISearch_RequestLogNeverContainsTheQueryText(t *testing.T) {
	// GET /api/search?q=<query> is logged like any other request, but the
	// query string carries the user's search text — see requestLogger in
	// server.go, which redacts it the same way /dl/{token} is redacted,
	// because web-svc-startup.log is never rotated.
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

	logs := captureSlog(t)
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=very-secret-search-text", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	logged := logs.String()
	if strings.Contains(logged, "very-secret-search-text") {
		t.Errorf("request log leaked the search query text:\n%s", logged)
	}
	// Still logged, just redacted — dropping the line entirely would trade
	// one problem for a blind spot on a route people actually use.
	if !strings.Contains(logged, "/api/search?q=<redacted>") {
		t.Errorf("request log missing the redacted search path:\n%s", logged)
	}
	if !strings.Contains(logged, "method=GET") || !strings.Contains(logged, "status=200") {
		t.Errorf("request log missing method/status:\n%s", logged)
	}
}

func TestAPISearch_TransportFailure_LogsWithoutQueryText(t *testing.T) {
	// A transport-level failure (connection refused, in this test) wraps a
	// *url.Error whose Error() string embeds the full outbound Brave URL,
	// query string included. The handler must not echo err.Error() into
	// the never-rotated startup log, independent of the request-log
	// redaction covered above.
	brave := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	brave.Close() // closed before any request is made: connections refuse

	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		BraveSearchAPIKey:  "test-key",
		BraveSearchBaseURL: brave.URL,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	logs := captureSlog(t)
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=leaky-query-text", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body=%s", rec.Code, rec.Body.String())
	}
	logged := logs.String()
	if strings.Contains(logged, "leaky-query-text") {
		t.Errorf("request log leaked the search query via a raw transport error:\n%s", logged)
	}
}
