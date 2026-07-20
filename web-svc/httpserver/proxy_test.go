package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/web-svc/auth"
	"soulman/web-svc/httpserver"
)

func TestAPIEpisodes_ProxiesMemorySvcAndPassesLimit(t *testing.T) {
	var gotPath, gotQuery string
	memory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":1,"summary":"test episode"}]`))
	}))
	defer memory.Close()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", MemorySvcURL: memory.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/episodes?limit=5", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/memory/episodes" {
		t.Errorf("proxied path = %q, want /memory/episodes", gotPath)
	}
	if gotQuery != "limit=5" {
		t.Errorf("proxied query = %q, want limit=5", gotQuery)
	}
	if rec.Body.String() != `[{"id":1,"summary":"test episode"}]` {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestAPIRawInputs_ProxiesMemorySvc(t *testing.T) {
	memory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer memory.Close()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", MemorySvcURL: memory.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/raw-inputs/recent", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestAPIEpisodes_MemorySvcDown_Returns502(t *testing.T) {
	memory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	memory.Close() // closed immediately: connection refused, simulating "down"

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", MemorySvcURL: memory.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/episodes", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestAPIEpisodes_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/episodes", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAPISystemMonitor_ProxiesPerceptionSvc(t *testing.T) {
	var gotPath string
	perception := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"type":"disk_space","key":"C:\\","severity":"ok","value_percent":42,"checked_at":"2026-07-20T00:00:00Z"}]`))
	}))
	defer perception.Close()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", PerceptionSvcURL: perception.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/system-monitor", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/api/system-monitor/status" {
		t.Errorf("proxied path = %q, want /api/system-monitor/status", gotPath)
	}
}

func TestAPISystemMonitor_PerceptionSvcDown_Returns502(t *testing.T) {
	perception := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	perception.Close() // closed immediately: connection refused, simulating "down"

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", PerceptionSvcURL: perception.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/system-monitor", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestAPISystemMonitor_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/system-monitor", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
