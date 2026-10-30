package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"soulman/web-svc/auth"
	"soulman/web-svc/httpserver"
)

func diaryDo(t *testing.T, root, method, path, body string, withToken bool) *httptest.ResponseRecorder {
	t.Helper()
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", DiaryRoot: root}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))
	var rd *bytes.Reader
	if body != "" {
		rd = bytes.NewReader([]byte(body))
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if withToken {
		req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func seedDiary(t *testing.T, root, date, name, content string) string {
	t.Helper()
	p := strings.Split(date, "-")
	dir := filepath.Join(root, p[0], p[1], p[2])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestAPIDiaryTree_ReturnsNestedStructure(t *testing.T) {
	root := t.TempDir()
	seedDiary(t, root, "2026-03-05", "entry-09-30-00.txt", "hi")
	rec := diaryDo(t, root, http.MethodGet, "/api/diary/tree", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	want := `{"years":[{"year":"2026","months":[{"month":"03","days":[{"day":"05","count":1}]}]}]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestAPIDiaryTree_EmptyRoot_ReturnsEmptyArrayNotNull(t *testing.T) {
	rec := diaryDo(t, t.TempDir(), http.MethodGet, "/api/diary/tree", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"years":[]}` {
		t.Errorf("body = %s", got)
	}
}

func TestAPIDiaryTree_MissingRoot_Returns503(t *testing.T) {
	rec := diaryDo(t, filepath.Join(t.TempDir(), "nope"), http.MethodGet, "/api/diary/tree", "", true)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestAPIDiaryEntries_ReturnsDayEntries(t *testing.T) {
	root := t.TempDir()
	seedDiary(t, root, "2026-03-05", "entry-09-30-00.txt", "hello")
	rec := diaryDo(t, root, http.MethodGet, "/api/diary/entries?date=2026-03-05", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	want := `{"entries":[{"name":"entry-09-30-00.txt","time":"09:30:00","content":"hello"}]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestAPIDiaryEntries_BadDate_Returns400(t *testing.T) {
	rec := diaryDo(t, t.TempDir(), http.MethodGet, "/api/diary/entries?date=../x", "", true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAPIDiaryEntries_MissingDay_Returns404(t *testing.T) {
	rec := diaryDo(t, t.TempDir(), http.MethodGet, "/api/diary/entries?date=2026-03-05", "", true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAPIDiaryCreate_Returns201AndWritesFile(t *testing.T) {
	root := t.TempDir()
	rec := diaryDo(t, root, http.MethodPost, "/api/diary/entries", `{"content":"dear diary"}`, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	p := strings.Split(body["date"], "-")
	if len(p) != 3 || body["name"] == "" || body["time"] == "" {
		t.Fatalf("bad body %v", body)
	}
	b, err := os.ReadFile(filepath.Join(root, p[0], p[1], p[2], body["name"]))
	if err != nil || string(b) != "dear diary" {
		t.Errorf("file = %q, err = %v", b, err)
	}
}

func TestAPIDiaryCreate_EmptyContent_Returns400(t *testing.T) {
	rec := diaryDo(t, t.TempDir(), http.MethodPost, "/api/diary/entries", `{"content":"  "}`, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAPIDiaryUpdate_Returns204AndOverwrites(t *testing.T) {
	root := t.TempDir()
	full := seedDiary(t, root, "2026-03-05", "entry-09-30-00.txt", "old")
	rec := diaryDo(t, root, http.MethodPut, "/api/diary/entries",
		`{"date":"2026-03-05","name":"entry-09-30-00.txt","content":"new"}`, true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if b, _ := os.ReadFile(full); string(b) != "new" {
		t.Errorf("file = %q", b)
	}
}

func TestAPIDiaryUpdate_TraversalName_Returns400(t *testing.T) {
	rec := diaryDo(t, t.TempDir(), http.MethodPut, "/api/diary/entries",
		`{"date":"2026-03-05","name":"../../x.txt","content":"z"}`, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAPIDiary_NoToken_Returns401(t *testing.T) {
	rec := diaryDo(t, t.TempDir(), http.MethodGet, "/api/diary/tree", "", false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
