package sysmonitor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// serviceHealthTimeout bounds every service_health dial/GET — generous
// enough to avoid false positives on a slow-but-alive local service, short
// enough not to stall the poll loop. Not configurable per check (see design
// spec's Out of Scope section).
const serviceHealthTimeout = 5 * time.Second

// healthChecker is the seam between runCheck and the actual network probe
// for service_health checks. Deliberately separate from statsProvider,
// which mirrors local OS syscalls (golang.org/x/sys/windows) — this is
// network I/O with its own failure modes (timeouts, DNS, refused
// connections, HTTP status codes). Tests inject a fake; httpTCPHealthChecker
// is the real implementation.
type healthChecker interface {
	Check(target string, timeout time.Duration) (healthy bool, detail string)
}

// httpTCPHealthChecker implements healthChecker. target is polymorphic,
// detected by prefix: "http://"/"https://" issues a GET and treats any 2xx
// status as healthy; anything else is treated as "host:port" for a raw TCP
// dial. See docs/superpowers/specs/2026-07-19-system-monitor-service-health-design.md.
type httpTCPHealthChecker struct{}

func (httpTCPHealthChecker) Check(target string, timeout time.Duration) (bool, string) {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return checkHTTP(target, timeout)
	}
	return checkTCP(target, timeout)
}

func checkHTTP(target string, timeout time.Duration) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false, err.Error()
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, ""
	}
	return false, fmt.Sprintf("status %d", resp.StatusCode)
}

func checkTCP(target string, timeout time.Duration) (bool, string) {
	conn, err := net.DialTimeout("tcp", target, timeout)
	if err != nil {
		return false, err.Error()
	}
	conn.Close()
	return true, ""
}
