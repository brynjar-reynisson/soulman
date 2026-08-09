package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"soulman/web-svc/auth"
	"soulman/web-svc/claudesession"
	"soulman/web-svc/httpserver"
)

func TestAPIClaudeRoots_ReturnsRootsWithExistenceAndFolders(t *testing.T) {
	existing := t.TempDir()
	os.Mkdir(filepath.Join(existing, "soulman"), 0o755)
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		ClaudeProjectRoots: []claudesession.Root{
			{Label: "Obsidian", Path: existing},
			{Label: "Misc Projects", Path: missing},
		},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/claude/roots", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Roots []struct {
			Label   string   `json:"label"`
			Exists  bool     `json:"exists"`
			Folders []string `json:"folders"`
		} `json:"roots"`
	}
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body.Roots) != 2 {
		t.Fatalf("len(roots) = %d, want 2", len(body.Roots))
	}
	if body.Roots[0].Label != "Obsidian" || !body.Roots[0].Exists || len(body.Roots[0].Folders) != 1 || body.Roots[0].Folders[0] != "soulman" {
		t.Errorf("roots[0] = %+v, want Obsidian/exists/[soulman]", body.Roots[0])
	}
	if body.Roots[1].Label != "Misc Projects" || body.Roots[1].Exists {
		t.Errorf("roots[1] = %+v, want Misc Projects/not exists", body.Roots[1])
	}
}

func TestAPIClaudeRoots_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))

	req := httptest.NewRequest(http.MethodGet, "/api/claude/roots", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAPIClaudeLaunch_UnknownRoot_Returns400(t *testing.T) {
	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		ClaudeProjectRoots: []claudesession.Root{{Label: "Obsidian", Path: t.TempDir()}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"root": "NotConfigured", "folder": "x", "sessionName": "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/claude/launch", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIClaudeLaunch_InvalidFolder_Returns400(t *testing.T) {
	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		ClaudeProjectRoots: []claudesession.Root{{Label: "Obsidian", Path: t.TempDir()}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"root": "Obsidian", "folder": "../etc", "sessionName": "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/claude/launch", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIClaudeLaunch_MissingFolder_Returns404(t *testing.T) {
	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		ClaudeProjectRoots: []claudesession.Root{{Label: "Obsidian", Path: t.TempDir()}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"root": "Obsidian", "folder": "does-not-exist", "sessionName": "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/claude/launch", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIClaudeLaunch_InvalidBody_Returns400(t *testing.T) {
	cfg := httpserver.Config{
		CORSAllowedOrigin:  "http://localhost:5178",
		ClaudeProjectRoots: []claudesession.Root{{Label: "Obsidian", Path: t.TempDir()}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodPost, "/api/claude/launch", bytes.NewReader([]byte("not json")))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIClaudeLaunch_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))

	req := httptest.NewRequest(http.MethodPost, "/api/claude/launch", bytes.NewReader([]byte("{}")))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
