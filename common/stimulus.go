// Package common holds the wire-format types shared across Soulman's
// services (Stimulus, crossing Perception -> Thinking -> Memory, and
// ActionRequest, crossing Thinking -> Action). These used to be hand-copied
// into each service's own model package; a mismatch between thinking-svc's
// and action-svc's independently-written ActionRequest shapes caused a real
// bug (action-svc silently dropped every request) that only surfaced during
// live end-to-end testing. This package is the single source of truth going
// forward — services import it via a local `replace` directive rather than
// redefining these types.
package common

import (
	"encoding/json"
	"time"
)

// Stimulus is the canonical format for everything crossing the
// Perception -> Thinking boundary. See "Perception module.md"'s Stimulus
// schema for the full field-by-field rationale.
type Stimulus struct {
	StimulusID    string      `json:"stimulus_id"`
	SchemaVersion int         `json:"schema_version"`
	ReceivedAt    time.Time   `json:"received_at"`
	OccurredAt    *time.Time  `json:"occurred_at,omitempty"`
	Channel       string      `json:"channel"`
	Source        Source      `json:"source"`
	Content       Content     `json:"content"`
	ChannelMeta   ChannelMeta `json:"channel_metadata"`
	Hints         Hints       `json:"hints"`
	Override      Override    `json:"override"`
}

type Source struct {
	Identity      string `json:"identity"`
	Authenticated bool   `json:"authenticated"`
	AuthMethod    string `json:"auth_method"`
}

type Content struct {
	RawText     string          `json:"raw_text"`
	RawPayload  json.RawMessage `json:"raw_payload"`
	ContentType string          `json:"content_type"`
	Attachments []Attachment    `json:"attachments"`
}

type Attachment struct {
	Filename  string `json:"filename"`
	MIMEType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	URI       string `json:"uri"`
}

type ChannelMeta struct {
	MessageID       string          `json:"message_id"`
	ThreadID        string          `json:"thread_id"`
	ReplyTo         string          `json:"reply_to"`
	ChannelSpecific json.RawMessage `json:"channel_specific"`
}

type Hints struct {
	Intent   *string  `json:"intent"`
	Priority string   `json:"priority"`
	Tags     []string `json:"tags"`
}

type Override struct {
	IsOverride bool            `json:"is_override"`
	Command    *string         `json:"command"`
	Params     json.RawMessage `json:"params"`
}
