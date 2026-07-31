package sysmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// internalHealthDependency mirrors one entry of a soulman service's
// /health response body's "dependencies" map. See
// docs/superpowers/specs/2026-07-27-dependency-health-design.md.
type internalHealthDependency struct {
	Status string `json:"status"`
	Since  string `json:"since,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// internalHealthBody mirrors a soulman service's /health response shape.
type internalHealthBody struct {
	Status       string                              `json:"status"` // parsed but intentionally unused: severity is derived per-dependency below, to avoid double-reporting
	Dependencies map[string]internalHealthDependency `json:"dependencies"`
}

// internalHealthChecker is the seam between runInternalHealthCheck and
// the actual HTTP GET — mirrors healthChecker's separation for
// service_health. Tests inject a fake; httpInternalHealthChecker is the
// real implementation.
type internalHealthChecker interface {
	FetchHealth(target string, timeout time.Duration) (*internalHealthBody, error)
}

type httpInternalHealthChecker struct{}

func (httpInternalHealthChecker) FetchHealth(target string, timeout time.Duration) (*internalHealthBody, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var body internalHealthBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &body, nil
}
