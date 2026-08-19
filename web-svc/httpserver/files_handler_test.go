// web-svc/httpserver/files_handler_test.go
package httpserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestAPIFilesDownload_ServesFileBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/download?root=Documents&path=&file=note.txt", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "hello world" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "hello world")
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="note.txt"` {
		t.Errorf("Content-Disposition = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store — a stale cached download would silently mask any future fix to this handler", got)
	}
}

func TestAPIFilesDownload_NonASCIIText_PrependsUTF8BOM(t *testing.T) {
	dir := t.TempDir()
	content := "Sæng, kodda — þægilegt á 90 cm rúm"
	if err := os.WriteFile(filepath.Join(dir, "checklist.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/download?root=Documents&path=&file=checklist.txt", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.Bytes()
	wantBOM := []byte{0xEF, 0xBB, 0xBF}
	if len(body) < 3 || string(body[:3]) != string(wantBOM) {
		t.Fatalf("body does not start with a UTF-8 BOM: %x", body[:min(3, len(body))])
	}
	if string(body[3:]) != content {
		t.Errorf("body after BOM = %q, want %q", body[3:], content)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/plain; charset=utf-8", got)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(content)+3) {
		t.Errorf("Content-Length = %q, want %d", got, len(content)+3)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestAPIFilesDownload_ASCIIOnlyText_NoBOMAdded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("plain ascii, no accents here"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/download?root=Documents&path=&file=note.txt", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "plain ascii, no accents here" {
		t.Errorf("body = %q, want unmodified ASCII content (no BOM)", rec.Body.String())
	}
}

func TestAPIFilesDownload_AlreadyHasBOM_NotDoubled(t *testing.T) {
	dir := t.TempDir()
	existingBOM := []byte{0xEF, 0xBB, 0xBF}
	content := append(append([]byte{}, existingBOM...), []byte("þegar með BOM")...)
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/download?root=Documents&path=&file=note.txt", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), content) {
		t.Errorf("body = %x, want unmodified original bytes %x (no second BOM)", rec.Body.Bytes(), content)
	}
}

func TestAPIFilesDownload_BinaryFile_NoBOMAdded(t *testing.T) {
	dir := t.TempDir()
	// PNG magic bytes followed by non-ASCII binary payload — must not be
	// mistaken for UTF-8 text just because it contains bytes >= 0x80.
	binary := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x01, 0x02, 0xFF, 0xFE}
	if err := os.WriteFile(filepath.Join(dir, "image.png"), binary, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/download?root=Documents&path=&file=image.png", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), binary) {
		t.Errorf("body = %x, want unmodified binary bytes %x", rec.Body.Bytes(), binary)
	}
}

func TestAPIFilesDownload_MissingFile_Returns404(t *testing.T) {
	dir := t.TempDir()
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/files/download?root=Documents&path=&file=missing.txt", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

func newUploadRequest(t *testing.T, url, fieldName, filename string, content []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("writing multipart content: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestAPIFilesUpload_NewFile_Returns200AndWritesContent(t *testing.T) {
	dir := t.TempDir()
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := newUploadRequest(t, "/api/files/upload?root=Documents&path=&overwrite=false", "file", "note.txt", []byte("hello"))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil {
		t.Fatalf("reading uploaded file: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want hello", got)
	}
}

func TestAPIFilesUpload_ExistingFileNoOverwrite_Returns409(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("original"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := newUploadRequest(t, "/api/files/upload?root=Documents&path=&overwrite=false", "file", "note.txt", []byte("new"))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIFilesUpload_OversizedBody_Returns413(t *testing.T) {
	dir := t.TempDir()
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	// Streams a body one byte over the 200MB cap via io.Pipe, so the test
	// doesn't have to hold the whole oversized payload in memory at once.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("file", "huge.bin")
		if err == nil {
			_, _ = io.Copy(part, io.LimitReader(zeroReader{}, 200<<20+1))
		}
		mw.Close()
		pw.Close()
	}()

	req := httptest.NewRequest(http.MethodPost, "/api/files/upload?root=Documents&path=&overwrite=false", pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413, body=%s", rec.Code, rec.Body.String())
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestAPIFilesUpload_UnknownRoot_Returns400(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := newUploadRequest(t, "/api/files/upload?root=NoSuchRoot&path=&overwrite=false", "file", "note.txt", []byte("hi"))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIFilesShare_ReturnsURLAndExpiry(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
		ShareLinkSecret:   []byte("test-secret-32-bytes-long-abcdef"),
		ShareLinkTTL:      time.Hour,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodPost, "/api/files/share?root=Documents&path=&file=note.txt", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		URL       string `json:"url"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(body.URL, "/dl/") {
		t.Errorf("url = %q, want a /dl/ prefix", body.URL)
	}
	if body.ExpiresAt == "" {
		t.Errorf("expiresAt is empty")
	}
}

func TestAPIFilesShare_MissingFile_Returns404(t *testing.T) {
	dir := t.TempDir()
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
		ShareLinkSecret:   []byte("test-secret-32-bytes-long-abcdef"),
		ShareLinkTTL:      time.Hour,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodPost, "/api/files/share?root=Documents&path=&file=missing.txt", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIFilesShare_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))
	req := httptest.NewRequest(http.MethodPost, "/api/files/share?root=Documents&path=&file=note.txt", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
