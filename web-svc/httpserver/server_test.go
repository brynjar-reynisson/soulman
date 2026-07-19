package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"soulman/web-svc/auth"
	"soulman/web-svc/httpserver"
)

const (
	testSupabaseURL = "https://example.supabase.co"
	testSecret      = "test-secret"
	testOwnerEmail  = "breynisson@gmail.com"
)

func newTestUpstream(t *testing.T, healthy bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
}

func TestHealth_ReturnsOK(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestAPIStatus_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAPIStatus_AllUpstreamsHealthy_ReportsUp(t *testing.T) {
	perception := newTestUpstream(t, true)
	defer perception.Close()
	memory := newTestUpstream(t, true)
	defer memory.Close()
	thinking := newTestUpstream(t, true)
	defer thinking.Close()
	action := newTestUpstream(t, true)
	defer action.Close()

	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		PerceptionSvcURL:  perception.URL,
		MemorySvcURL:      memory.URL,
		ThinkingSvcURL:    thinking.URL,
		ActionSvcURL:      action.URL,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, name := range []string{"perception-svc", "memory-svc", "thinking-svc", "action-svc"} {
		if body[name] != "up" {
			t.Errorf("%s = %q, want up", name, body[name])
		}
	}
}

func TestAPIStatus_OneUpstreamDown_ReportsDownWithout500(t *testing.T) {
	perception := newTestUpstream(t, true)
	defer perception.Close()
	memory := newTestUpstream(t, false)
	defer memory.Close()
	thinking := newTestUpstream(t, true)
	defer thinking.Close()
	action := newTestUpstream(t, true)
	defer action.Close()

	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		PerceptionSvcURL:  perception.URL,
		MemorySvcURL:      memory.URL,
		ThinkingSvcURL:    thinking.URL,
		ActionSvcURL:      action.URL,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with a downed service", rec.Code)
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["memory-svc"] != "down" {
		t.Errorf("memory-svc = %q, want down", body["memory-svc"])
	}
	if body["perception-svc"] != "up" {
		t.Errorf("perception-svc = %q, want up", body["perception-svc"])
	}
}

func ownerToken(t *testing.T) string {
	t.Helper()
	// Mirrors auth_test.go's hsToken helper — duplicated here (rather than
	// exported from the auth package) since it's test-only fixture code,
	// consistent with how each package's tests build their own fixtures.
	return signHS256(t, testSecret, testSupabaseURL+"/auth/v1", "authenticated", testOwnerEmail)
}

func signHS256(t *testing.T, secret, issuer, audience, email string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":   issuer,
		"aud":   audience,
		"email": email,
		"sub":   "test-user",
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	return signed
}
