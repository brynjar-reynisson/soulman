package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"soulman/common"
)

type cliRequest struct {
	Text     string `json:"text"`
	Mode     string `json:"mode"`
	Priority string `json:"priority"`
}

var validCLIPriorities = map[string]bool{"low": true, "normal": true, "high": true, "critical": true}

// perceiveCLI implements docs/superpowers/specs/2026-07-18-soulman-cli-design.md's
// POST /api/perceive/cli endpoint — the CLI push channel from
// Perception module.md. mode "note" produces a channel: "cli-note" stimulus
// (thinking-svc's CLINoteRule handles it mechanically); mode "stimulus"
// (the default) produces channel: "cli" for future goal-driven reasoning.
func (s *Server) perceiveCLI(w http.ResponseWriter, r *http.Request) {
	var req cliRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeCLIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if strings.TrimSpace(req.Text) == "" {
		writeCLIError(w, http.StatusBadRequest, "text is required")
		return
	}
	if req.Mode == "" {
		req.Mode = "stimulus"
	}
	if req.Mode != "stimulus" && req.Mode != "note" {
		writeCLIError(w, http.StatusBadRequest, `mode must be "note" or "stimulus"`)
		return
	}
	if req.Priority == "" {
		req.Priority = "normal"
	}
	if !validCLIPriorities[req.Priority] {
		writeCLIError(w, http.StatusBadRequest, "priority must be one of low, normal, high, critical")
		return
	}

	stimulus := buildCLIStimulus(req)

	if err := s.publisher.Publish(r.Context(), stimulus); err != nil {
		writeCLIError(w, http.StatusServiceUnavailable, "failed to publish stimulus")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"stimulus_id": stimulus.StimulusID})
}

func writeCLIError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func buildCLIStimulus(req cliRequest) *common.Stimulus {
	channel := "cli"
	if req.Mode == "note" {
		channel = "cli-note"
	}

	now := time.Now().UTC()
	id, err := uuid.NewV7()
	if err != nil {
		// Extremely unlikely (crypto/rand failure); fall back to a random v4
		// rather than fail the request over id generation.
		id = uuid.New()
	}

	return &common.Stimulus{
		StimulusID:    id.String(),
		SchemaVersion: 1,
		ReceivedAt:    now,
		OccurredAt:    &now,
		Channel:       channel,
		Source: common.Source{
			Identity:      "cli",
			Authenticated: true,
			AuthMethod:    "system",
		},
		Content: common.Content{
			RawText:     req.Text,
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
			Attachments: []common.Attachment{},
		},
		ChannelMeta: common.ChannelMeta{
			MessageID: computeCLIMessageID(req.Text, now),
		},
		Hints: common.Hints{
			Priority: req.Priority,
			Tags:     []string{},
		},
		Override: common.Override{
			IsOverride: false,
			Params:     json.RawMessage(`{}`),
		},
	}
}

// computeCLIMessageID gives downstream consumers a stable dedup key. CLI
// input has no natural external id (unlike folder-watcher's
// filename+mtime), so this hashes the text plus received-at timestamp.
func computeCLIMessageID(text string, receivedAt time.Time) string {
	sum := sha256.Sum256([]byte(text + receivedAt.Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])
}
