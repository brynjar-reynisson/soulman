package sysmonitor

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPTCPHealthChecker_HTTPHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	healthy, detail := (httpTCPHealthChecker{}).Check(srv.URL, time.Second)
	if !healthy {
		t.Errorf("healthy = false, want true (detail=%q)", detail)
	}
}

func TestHTTPTCPHealthChecker_HTTPUnhealthyStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	healthy, detail := (httpTCPHealthChecker{}).Check(srv.URL, time.Second)
	if healthy {
		t.Error("healthy = true, want false for a 503 response")
	}
	if detail != "status 503" {
		t.Errorf("detail = %q, want %q", detail, "status 503")
	}
}

func TestHTTPTCPHealthChecker_HTTPUnreachable(t *testing.T) {
	// Nothing listens on this port: 127.0.0.1:1 is a reserved low port
	// that refuses connections immediately rather than timing out.
	healthy, detail := (httpTCPHealthChecker{}).Check("http://127.0.0.1:1/health", time.Second)
	if healthy {
		t.Error("healthy = true, want false for an unreachable HTTP target")
	}
	if detail == "" {
		t.Error("detail = empty, want a non-empty error description")
	}
}

func TestHTTPTCPHealthChecker_TCPHealthy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	healthy, detail := (httpTCPHealthChecker{}).Check(ln.Addr().String(), time.Second)
	if !healthy {
		t.Errorf("healthy = false, want true (detail=%q)", detail)
	}
}

func TestHTTPTCPHealthChecker_TCPUnhealthy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close() // closed immediately: nothing listens on addr anymore

	healthy, detail := (httpTCPHealthChecker{}).Check(addr, time.Second)
	if healthy {
		t.Error("healthy = true, want false for a closed port")
	}
	if detail == "" {
		t.Error("detail = empty, want a non-empty error description")
	}
}
