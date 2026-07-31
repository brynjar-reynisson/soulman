package httpserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/common/dephealth"
	"soulman/memory-svc/httpserver"
	"soulman/memory-svc/storage"
)

// newTestServer builds a Server whose db is always disconnected (a nil
// *storage.DB wrapped in its own throwaway registry) — none of these
// tests exercise a real Postgres call, so what matters is reg, the
// registry actually passed to New, which independently drives /health.
func newTestServer(reg *dephealth.Registry) *httpserver.Server {
	return httpserver.New(storage.NewDBHolder(nil, dephealth.NewRegistry()), reg, "9002")
}

func TestHealth_AllDependenciesOK(t *testing.T) {
	reg := dephealth.NewRegistry()
	reg.Record("postgres", nil)
	srv := newTestServer(reg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Status       string                    `json:"status"`
		Dependencies map[string]map[string]any `json:"dependencies"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.Dependencies["postgres"]["status"] != "ok" {
		t.Errorf(`dependencies.postgres.status = %v, want "ok"`, body.Dependencies["postgres"]["status"])
	}
}

func TestHealth_DependencyDown_ReportsDegraded(t *testing.T) {
	reg := dephealth.NewRegistry()
	reg.Record("postgres", errors.New("dial tcp 127.0.0.1:54322: connect: connection refused"))
	srv := newTestServer(reg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Status       string                    `json:"status"`
		Dependencies map[string]map[string]any `json:"dependencies"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "degraded" {
		t.Errorf("status = %q, want degraded", body.Status)
	}
	dep := body.Dependencies["postgres"]
	if dep["status"] != "down" {
		t.Errorf(`dependencies.postgres.status = %v, want "down"`, dep["status"])
	}
	if dep["since"] == nil || dep["since"] == "" {
		t.Error("dependencies.postgres.since missing, want a timestamp")
	}
	if dep["detail"] == nil || dep["detail"] == "" {
		t.Error("dependencies.postgres.detail missing, want the error text")
	}
}

func TestRawInputsRecent_NotConnected_Returns503(t *testing.T) {
	srv := newTestServer(dephealth.NewRegistry())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/raw-inputs/recent", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestMemoryStubs_Return501(t *testing.T) {
	srv := newTestServer(dephealth.NewRegistry())
	paths := []string{"/memory/search", "/memory/procedures", "/memory/goals"}

	for _, path := range paths {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, want 501", path, rec.Code)
		}
	}
}

func TestMemoryEpisodes_NotConnected_Returns503(t *testing.T) {
	srv := newTestServer(dephealth.NewRegistry())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/memory/episodes", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestRawInputsRecent_DefaultLimit(t *testing.T) {
	srv := newTestServer(dephealth.NewRegistry())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/raw-inputs/recent?limit=abc", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusBadRequest {
		t.Error("bad limit param should be silently ignored, not return 400")
	}
}

func TestMemoryEpisodes_DefaultLimit(t *testing.T) {
	srv := newTestServer(dephealth.NewRegistry())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/memory/episodes?limit=abc", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusBadRequest {
		t.Error("bad limit param should be silently ignored, not return 400")
	}
}
