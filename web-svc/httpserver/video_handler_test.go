package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"soulman/web-svc/auth"
	"soulman/web-svc/httpserver"
	"soulman/web-svc/sharelink"
)

var videoSecret = []byte("0123456789abcdef0123456789abcdef")

func newVideoServer(t *testing.T) (*httpserver.Server, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "trips"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "trips", "beach.mp4"), []byte("0123456789"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		VideoRoot:         dir,
		VideoShareSecret:  videoSecret,
		VideoShareTTL:     7 * 24 * time.Hour,
	}
	return httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)), dir
}

func do(t *testing.T, srv *httpserver.Server, method, url string, owner bool, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, nil)
	if owner {
		req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestVideosList_OnlyVideosAndFolders_RequiresAuth(t *testing.T) {
	srv, _ := newVideoServer(t)
	if rec := do(t, srv, "GET", "/api/videos/list", false, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
	}
	rec := do(t, srv, "GET", "/api/videos/list?path=", true, nil)
	var body struct {
		Folders []string                `json:"folders"`
		Files   []struct{ Name string } `json:"files"`
	}
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body.Folders) != 1 || body.Folders[0] != "trips" || len(body.Files) != 0 {
		t.Errorf("root listing = %+v, want folder trips and no files (notes.txt filtered)", body)
	}
	rec = do(t, srv, "GET", "/api/videos/list?path=trips", true, nil)
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body.Files) != 1 || body.Files[0].Name != "beach.mp4" {
		t.Errorf("trips listing = %+v", body)
	}
}

func issueToken(t *testing.T, srv *httpserver.Server) string {
	t.Helper()
	rec := do(t, srv, "POST", "/api/videos/token?path=trips&file=beach.mp4", true, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("token status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct{ Token string }
	json.NewDecoder(rec.Body).Decode(&body)
	if body.Token == "" {
		t.Fatal("empty token")
	}
	return body.Token
}

func TestVideoToken_RejectsNonVideoAndMissing(t *testing.T) {
	srv, _ := newVideoServer(t)
	if rec := do(t, srv, "POST", "/api/videos/token?path=&file=notes.txt", true, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("non-video status = %d, want 400", rec.Code)
	}
	if rec := do(t, srv, "POST", "/api/videos/token?path=&file=gone.mp4", true, nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing status = %d, want 404", rec.Code)
	}
	if rec := do(t, srv, "POST", "/api/videos/token?path=../x&file=a.mp4", true, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("traversal status = %d, want 400", rec.Code)
	}
}

func TestVideoStream_PublicWithToken_SupportsRange(t *testing.T) {
	srv, _ := newVideoServer(t)
	tok := issueToken(t, srv)

	info := do(t, srv, "GET", "/vid/"+tok+"/info", false, nil)
	if info.Code != http.StatusOK || !strings.Contains(info.Body.String(), "beach.mp4") {
		t.Errorf("info = %d %s", info.Code, info.Body.String())
	}
	rec := do(t, srv, "GET", "/vid/"+tok+"/stream", false, map[string]string{"Range": "bytes=2-5"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "2345" {
		t.Fatalf("range = %d %q, want 206 \"2345\"", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestVideoStream_BadTokens(t *testing.T) {
	srv, _ := newVideoServer(t)
	tok := issueToken(t, srv)
	if rec := do(t, srv, "GET", "/vid/"+tok+"x/stream", false, nil); rec.Code != http.StatusNotFound {
		t.Errorf("tampered status = %d, want 404", rec.Code)
	}
	expired, _ := sharelink.Issue(videoSecret, "videos", "trips", "beach.mp4", -time.Minute)
	if rec := do(t, srv, "GET", "/vid/"+expired+"/stream", false, nil); rec.Code != http.StatusGone {
		t.Errorf("expired status = %d, want 410", rec.Code)
	}
	wrongRoot, _ := sharelink.Issue(videoSecret, "Documents", "trips", "beach.mp4", time.Hour)
	if rec := do(t, srv, "GET", "/vid/"+wrongRoot+"/stream", false, nil); rec.Code != http.StatusNotFound {
		t.Errorf("wrong-root status = %d, want 404", rec.Code)
	}
	nonVideo, _ := sharelink.Issue(videoSecret, "videos", "", "notes.txt", time.Hour)
	if rec := do(t, srv, "GET", "/vid/"+nonVideo+"/stream", false, nil); rec.Code != http.StatusNotFound {
		t.Errorf("non-video token status = %d, want 404", rec.Code)
	}
}

func TestVideoStream_RateLimitsRepeatedFailures(t *testing.T) {
	srv, _ := newVideoServer(t)
	hdr := map[string]string{"CF-Connecting-IP": "203.0.113.9"}
	var last int
	for i := 0; i < 25; i++ {
		last = do(t, srv, "GET", "/vid/bogus/stream", false, hdr).Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("after 25 failures status = %d, want 429", last)
	}
	// A different client is unaffected.
	if code := do(t, srv, "GET", "/vid/bogus/stream", false, map[string]string{"CF-Connecting-IP": "198.51.100.1"}).Code; code != http.StatusNotFound {
		t.Errorf("other client status = %d, want 404", code)
	}
}
