package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"soulman/web-svc/auth"
	"soulman/web-svc/httpserver"
)

func writeReportFile(t *testing.T, root string, date time.Time, content string) {
	t.Helper()
	dir := filepath.Join(root, "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	filename := "daily-report-" + date.Format("2006-01-02") + ".txt"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
		t.Fatalf("writing report file: %v", err)
	}
}

func TestAPIReportsLatest_ReturnsTodaysReport(t *testing.T) {
	root := t.TempDir()
	writeReportFile(t, root, time.Now(), "today's report")

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ReportsRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/reports/latest", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	want := "## Important\n\ntoday's report\n"
	if body["content"] != want {
		t.Errorf("content = %q, want %q", body["content"], want)
	}
}

func TestAPIReportsLatest_FallsBackToMostRecentWithinAWeek(t *testing.T) {
	root := t.TempDir()
	writeReportFile(t, root, time.Now().AddDate(0, 0, -3), "three days ago")

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ReportsRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/reports/latest", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	want := "## Important\n\nthree days ago\n"
	if body["content"] != want {
		t.Errorf("content = %q, want %q", body["content"], want)
	}
}

func TestAPIReportsLatest_NoReportInLastWeek_Returns404(t *testing.T) {
	root := t.TempDir()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ReportsRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/reports/latest", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAPIReportsByDate_ExistingDate_ReturnsContent(t *testing.T) {
	root := t.TempDir()
	date := time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)
	writeReportFile(t, root, date, "june first report")

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ReportsRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/reports?date=2026-06-01", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	wantContent := "## Important\n\njune first report\n"
	if body["content"] != wantContent {
		t.Errorf("content = %q, want %q", body["content"], wantContent)
	}
	if body["date"] != "2026-06-01" {
		t.Errorf("date = %q", body["date"])
	}
}

func TestAPIReportsByDate_MissingDate_Returns404(t *testing.T) {
	root := t.TempDir()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ReportsRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/reports?date=2020-01-01", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAPIReportsByDate_InvalidDateFormat_Returns400(t *testing.T) {
	root := t.TempDir()

	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178", ReportsRoot: root}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/reports?date=not-a-date", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
