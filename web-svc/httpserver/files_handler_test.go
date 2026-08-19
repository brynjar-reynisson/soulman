// web-svc/httpserver/files_handler_test.go
package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestAPIFilesList_ReturnsFoldersAndFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Taxes"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/list?root=Documents&path=", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Folders []string `json:"folders"`
		Files   []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"files"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Folders) != 1 || body.Folders[0] != "Taxes" {
		t.Errorf("folders = %v, want [Taxes]", body.Folders)
	}
	if len(body.Files) != 1 || body.Files[0].Name != "note.txt" || body.Files[0].Size != 5 {
		t.Errorf("files = %+v, want [{note.txt 5}]", body.Files)
	}
}

func TestAPIFilesList_UnknownRoot_Returns400(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/list?root=NoSuchRoot&path=", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIFilesList_PathTraversal_Returns400(t *testing.T) {
	dir := t.TempDir()
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/list?root=Documents&path=..%2F..%2Fwindows", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}
