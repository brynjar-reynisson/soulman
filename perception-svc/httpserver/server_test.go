package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/perception-svc/httpserver"
)

func TestHealth_ReportsStatusAndWatchedPaths(t *testing.T) {
	srv := httpserver.New("9001", []string{`C:\errors`, `C:\other`}, func() string { return "connected" }, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Status       string   `json:"status"`
		NATS         string   `json:"nats"`
		WatchedPaths []string `json:"watched_paths"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.NATS != "connected" {
		t.Errorf("nats = %q, want connected", body.NATS)
	}
	if len(body.WatchedPaths) != 2 {
		t.Errorf("watched_paths = %v, want 2 entries", body.WatchedPaths)
	}
}

func TestHealth_NilStatusFunc_DefaultsToDisconnected(t *testing.T) {
	srv := httpserver.New("9001", nil, nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	var body map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&body)
	if body["nats"] != "disconnected" {
		t.Errorf("nats = %v, want disconnected", body["nats"])
	}
	paths, ok := body["watched_paths"].([]interface{})
	if !ok || len(paths) != 0 {
		t.Errorf("watched_paths = %v, want empty array", body["watched_paths"])
	}
}
