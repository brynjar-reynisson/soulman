package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Request is the body sent to perception-svc's POST /api/perceive/cli
// endpoint, per docs/superpowers/specs/2026-07-18-soulman-cli-design.md.
type Request struct {
	Text     string `json:"text"`
	Mode     string `json:"mode"`
	Priority string `json:"priority"`
}

type response struct {
	StimulusID string `json:"stimulus_id"`
	Error      string `json:"error"`
}

// Send POSTs req to baseURL+"/api/perceive/cli" and returns the resulting
// stimulus_id, or an error describing why the request failed. There is no
// retry — this is an interactive CLI tool; the human re-running the
// command is the retry mechanism.
func Send(baseURL string, req Request) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("client: marshal request: %w", err)
	}

	resp, err := http.Post(baseURL+"/api/perceive/cli", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("client: request to %s failed: %w", baseURL, err)
	}
	defer resp.Body.Close()

	// Check status code before attempting to decode the body.
	if resp.StatusCode != http.StatusAccepted {
		// Best-effort attempt to decode error message from response body.
		var out response
		_ = json.NewDecoder(resp.Body).Decode(&out)

		msg := out.Error
		if msg == "" {
			msg = fmt.Sprintf("unexpected status %d", resp.StatusCode)
		}
		return "", fmt.Errorf("client: %s", strings.TrimSpace(msg))
	}

	// Status is 202; decode the body to get stimulus_id.
	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("client: decode response: %w", err)
	}

	return out.StimulusID, nil
}

// SendRaw POSTs the raw bytes of body (typically a file's contents,
// unparsed and unvalidated client-side — perception-svc's
// /api/perceive/raw endpoint owns all validation) to baseURL+"/api/perceive/raw"
// and returns the resulting stimulus_id, or an error describing why the
// request failed.
func SendRaw(baseURL string, body []byte) (string, error) {
	resp, err := http.Post(baseURL+"/api/perceive/raw", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("client: request to %s failed: %w", baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		var out response
		_ = json.NewDecoder(resp.Body).Decode(&out)

		msg := out.Error
		if msg == "" {
			msg = fmt.Sprintf("unexpected status %d", resp.StatusCode)
		}
		return "", fmt.Errorf("client: %s", strings.TrimSpace(msg))
	}

	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("client: decode response: %w", err)
	}

	return out.StimulusID, nil
}
