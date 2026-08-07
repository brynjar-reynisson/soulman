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
	"soulman/web-svc/httpserver"
)

func TestAPIObsidianFolders_ReturnsSortedDirectoryNames(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "zeta"), 0o755)
	os.Mkdir(filepath.Join(root, "alpha"), 0o755)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/obsidian/folders", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string][]string
	json.NewDecoder(rec.Body).Decode(&body)
	want := []string{"alpha", "zeta"}
	if len(body["folders"]) != 2 || body["folders"][0] != want[0] || body["folders"][1] != want[1] {
		t.Errorf("folders = %v, want %v", body["folders"], want)
	}
}

func TestAPIObsidianFolders_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: t.TempDir()}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))

	req := httptest.NewRequest(http.MethodGet, "/api/obsidian/folders", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAPIObsidianFiles_ReturnsFilesInFolder(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "note.md"), []byte("x"), 0o644)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/obsidian/files?folder=vault", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string][]string
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body["files"]) != 1 || body["files"][0] != "note.md" {
		t.Errorf("files = %v, want [note.md]", body["files"])
	}
}

func TestAPIObsidianFiles_PathTraversalFolder_Returns400(t *testing.T) {
	root := t.TempDir()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/obsidian/files?folder=..%2F..%2Fwindows", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIObsidianFile_ReturnsContent(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "note.md"), []byte("hello"), 0o644)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/obsidian/file?folder=vault&file=note.md", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["content"] != "hello" {
		t.Errorf("content = %q, want hello", body["content"])
	}
}

func TestAPIObsidianFile_Missing_Returns404(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "vault"), 0o755)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/obsidian/file?folder=vault&file=missing.md", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAPIObsidianFilePut_OverwritesExistingFile(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "note.md"), []byte("old"), 0o644)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"folder": "vault", "file": "note.md", "content": "new"})
	req := httptest.NewRequest(http.MethodPut, "/api/obsidian/file", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	b, _ := os.ReadFile(filepath.Join(folder, "note.md"))
	if string(b) != "new" {
		t.Errorf("file content = %q, want new", string(b))
	}
}

func TestAPIObsidianFilePut_MissingFile_Returns404(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "vault"), 0o755)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"folder": "vault", "file": "missing.md", "content": "x"})
	req := httptest.NewRequest(http.MethodPut, "/api/obsidian/file", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAPIObsidianFilePost_CreatesNewFile(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"folder": "vault", "file": "new.md", "content": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/api/obsidian/file", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	b, err := os.ReadFile(filepath.Join(folder, "new.md"))
	if err != nil || string(b) != "hello" {
		t.Errorf("file content = %q, err = %v", string(b), err)
	}
}

func TestAPIObsidianFilePost_AlreadyExists_Returns409(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "existing.md"), []byte("old"), 0o644)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"folder": "vault", "file": "existing.md", "content": "new"})
	req := httptest.NewRequest(http.MethodPost, "/api/obsidian/file", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestAPIObsidianFileRename_RenamesFile(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "old.md"), []byte("content"), 0o644)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"folder": "vault", "file": "old.md", "new_name": "new.md"})
	req := httptest.NewRequest(http.MethodPost, "/api/obsidian/file/rename", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(folder, "new.md")); err != nil {
		t.Errorf("new.md not found: %v", err)
	}
}

func TestAPIObsidianFileRename_DestinationExists_Returns409(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "vault")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "a.md"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(folder, "b.md"), []byte("b"), 0o644)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"folder": "vault", "file": "a.md", "new_name": "b.md"})
	req := httptest.NewRequest(http.MethodPost, "/api/obsidian/file/rename", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestAPIObsidianFileRename_SourceMissing_Returns404(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "vault"), 0o755)

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ObsidianRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	reqBody, _ := json.Marshal(map[string]string{"folder": "vault", "file": "missing.md", "new_name": "new.md"})
	req := httptest.NewRequest(http.MethodPost, "/api/obsidian/file/rename", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
