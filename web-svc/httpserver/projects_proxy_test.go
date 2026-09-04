package httpserver_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"soulman/web-svc/auth"
	"soulman/web-svc/httpserver"
)

func TestAPIProjects_ProxiesListToProjectsSvc(t *testing.T) {
	var gotMethod, gotPath string
	projectsSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer projectsSvc.Close()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ProjectsSvcURL: projectsSvc.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/projects", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotMethod != http.MethodGet || gotPath != "/projects" {
		t.Errorf("proxied request = %s %s, want GET /projects", gotMethod, gotPath)
	}
}

func TestAPIProjects_ProxiesCreateWithBodyAndMethod(t *testing.T) {
	var gotMethod string
	var gotBody []byte
	projectsSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer projectsSvc.Close()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ProjectsSvcURL: projectsSvc.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	wantBody := `{"name":"demo","path":"C:\\demo"}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/projects", strings.NewReader(wantBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	if gotMethod != http.MethodPost {
		t.Errorf("proxied method = %s, want POST", gotMethod)
	}
	if string(gotBody) != wantBody {
		t.Errorf("proxied body = %s, want %s", gotBody, wantBody)
	}
}

func TestAPIProjects_DeleteForwardsURLParam(t *testing.T) {
	var gotMethod, gotPath string
	projectsSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer projectsSvc.Close()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ProjectsSvcURL: projectsSvc.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodDelete, "/api/projects/projects/demo", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
	if gotMethod != http.MethodDelete || gotPath != "/projects/demo" {
		t.Errorf("proxied request = %s %s, want DELETE /projects/demo", gotMethod, gotPath)
	}
}

func TestAPIProjects_ProjectsSvcDown_Returns502(t *testing.T) {
	projectsSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	projectsSvc.Close() // closed immediately: connection refused, simulating "down"

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ProjectsSvcURL: projectsSvc.URL}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/projects", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestAPIProjects_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/projects", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
