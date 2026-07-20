package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"soulman/perception-svc/httpserver"
	"soulman/perception-svc/sysmonitor"
)

func TestHealth_ReportsStatusAndWatchedPaths(t *testing.T) {
	srv := httpserver.New("9001", []string{`C:\errors`, `C:\other`}, func() string { return "connected" }, nil, nil)
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
	srv := httpserver.New("9001", nil, nil, nil, nil)
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

func TestSystemMonitorStatus_ReturnsJSONArray(t *testing.T) {
	statusFn := func() []sysmonitor.CheckStatus {
		v := 42.0
		return []sysmonitor.CheckStatus{
			{Type: "disk_space", Key: `C:\`, Severity: "ok", ValuePercent: &v, CheckedAt: time.Now()},
		}
	}
	srv := httpserver.New("9001", nil, nil, nil, statusFn)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/system-monitor/status", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body []sysmonitor.CheckStatus
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 1 || body[0].Type != "disk_space" || body[0].Key != `C:\` {
		t.Errorf("body = %+v, want one disk_space C:\\ entry", body)
	}
}

func TestSystemMonitorStatus_NilFunc_ReturnsEmptyArray(t *testing.T) {
	srv := httpserver.New("9001", nil, nil, nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/system-monitor/status", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "[]\n" {
		t.Errorf("body = %q, want []\\n", rec.Body.String())
	}
}
