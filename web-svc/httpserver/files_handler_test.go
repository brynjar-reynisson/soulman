// web-svc/httpserver/files_handler_test.go
package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/web-svc/auth"
	"soulman/web-svc/filebrowser"
	"soulman/web-svc/httpserver"
)

func TestAPIFilesRoots_ReportsExistsPerRoot(t *testing.T) {
	dir := t.TempDir()
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots: []filebrowser.Root{
			{Label: "Documents", Path: dir},
			{Label: "Missing", Path: dir + `\does-not-exist`},
		},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/roots", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Roots []struct {
			Label  string `json:"label"`
			Exists bool   `json:"exists"`
		} `json:"roots"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Roots) != 2 {
		t.Fatalf("len(roots) = %d, want 2", len(body.Roots))
	}
	if body.Roots[0].Label != "Documents" || !body.Roots[0].Exists {
		t.Errorf("roots[0] = %+v, want Documents/exists=true", body.Roots[0])
	}
	if body.Roots[1].Exists {
		t.Errorf("roots[1].Exists = true, want false for a missing path")
	}
}

func TestAPIFilesRoots_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))
	req := httptest.NewRequest(http.MethodGet, "/api/files/roots", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
