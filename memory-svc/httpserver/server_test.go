package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/memory-svc/httpserver"
)

func TestHealth_NilDB(t *testing.T) {
	srv := httpserver.New(nil, "9002")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q, want ok", body["status"])
	}
	if body["db"] != "unavailable" {
		t.Errorf("db = %q, want unavailable", body["db"])
	}
}

func TestRawInputsRecent_NilDB_Returns503(t *testing.T) {
	srv := httpserver.New(nil, "9002")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/raw-inputs/recent", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestMemoryStubs_Return501(t *testing.T) {
	srv := httpserver.New(nil, "9002")
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

func TestMemoryEpisodes_NilDB_Returns503(t *testing.T) {
	srv := httpserver.New(nil, "9002")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/memory/episodes", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestRawInputsRecent_DefaultLimit(t *testing.T) {
	// Verify invalid limit is silently replaced with default (no 400 error)
	srv := httpserver.New(nil, "9002")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/raw-inputs/recent?limit=abc", nil)
	srv.Handler().ServeHTTP(rec, req)
	// Returns 503 because db is nil, not 400 — confirms limit parsing doesn't error
	if rec.Code == http.StatusBadRequest {
		t.Error("bad limit param should be silently ignored, not return 400")
	}
}

func TestMemoryEpisodes_DefaultLimit(t *testing.T) {
	srv := httpserver.New(nil, "9002")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/memory/episodes?limit=abc", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusBadRequest {
		t.Error("bad limit param should be silently ignored, not return 400")
	}
}
