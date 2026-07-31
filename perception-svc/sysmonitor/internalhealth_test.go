package sysmonitor

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPInternalHealthChecker_Healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","dependencies":{"postgres":{"status":"ok"}}}`))
	}))
	defer srv.Close()

	body, err := (httpInternalHealthChecker{}).FetchHealth(srv.URL, time.Second)
	if err != nil {
		t.Fatalf("FetchHealth: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("Status = %q, want ok", body.Status)
	}
	if body.Dependencies["postgres"].Status != "ok" {
		t.Errorf("Dependencies[postgres].Status = %q, want ok", body.Dependencies["postgres"].Status)
	}
}

func TestHTTPInternalHealthChecker_DegradedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"degraded","dependencies":{"postgres":{"status":"down","since":"2026-07-27T14:32:00Z","detail":"connection refused"}}}`))
	}))
	defer srv.Close()

	body, err := (httpInternalHealthChecker{}).FetchHealth(srv.URL, time.Second)
	if err != nil {
		t.Fatalf("FetchHealth: %v", err)
	}
	dep := body.Dependencies["postgres"]
	if dep.Status != "down" {
		t.Errorf("Dependencies[postgres].Status = %q, want down", dep.Status)
	}
	if dep.Detail != "connection refused" {
		t.Errorf("Dependencies[postgres].Detail = %q, want %q", dep.Detail, "connection refused")
	}
}

func TestHTTPInternalHealthChecker_UnhealthyStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, err := (httpInternalHealthChecker{}).FetchHealth(srv.URL, time.Second)
	if err == nil {
		t.Fatal("FetchHealth: want error for a 503 response, got nil")
	}
}

func TestHTTPInternalHealthChecker_Unreachable(t *testing.T) {
	// Nothing listens on this port: 127.0.0.1:1 is a reserved low port
	// that refuses connections immediately rather than timing out.
	_, err := (httpInternalHealthChecker{}).FetchHealth("http://127.0.0.1:1/health", time.Second)
	if err == nil {
		t.Fatal("FetchHealth: want error for an unreachable target, got nil")
	}
}

func TestHTTPInternalHealthChecker_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	_, err := (httpInternalHealthChecker{}).FetchHealth(srv.URL, time.Second)
	if err == nil {
		t.Fatal("FetchHealth: want error for invalid JSON body, got nil")
	}
}
