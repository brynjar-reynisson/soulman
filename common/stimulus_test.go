package common_test

import (
	"encoding/json"
	"testing"
	"time"

	"soulman/common"
)

func TestStimulus_JSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	occurred := now.Add(-time.Minute)
	s := common.Stimulus{
		StimulusID:    "018f1a2b-3c4d-7e8f-9a0b-1c2d3e4f5a6b",
		SchemaVersion: 1,
		ReceivedAt:    now,
		OccurredAt:    &occurred,
		Channel:       "folder-watcher",
		Source:        common.Source{Identity: "folder-watcher", Authenticated: true, AuthMethod: "system"},
		Content: common.Content{
			RawText:     "boom, something broke",
			ContentType: "text",
			RawPayload:  json.RawMessage(`{}`),
			Attachments: []common.Attachment{},
		},
		ChannelMeta: common.ChannelMeta{
			MessageID:       "abc123",
			ChannelSpecific: json.RawMessage(`{"watched_path":"C:\\errors"}`),
		},
		Hints:    common.Hints{Priority: "high", Tags: []string{"error", "folder-watcher"}},
		Override: common.Override{IsOverride: false, Params: json.RawMessage(`{}`)},
	}

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got common.Stimulus
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.StimulusID != s.StimulusID {
		t.Errorf("StimulusID = %q, want %q", got.StimulusID, s.StimulusID)
	}
	if got.Channel != s.Channel {
		t.Errorf("Channel = %q, want %q", got.Channel, s.Channel)
	}
	if got.Content.RawText != s.Content.RawText {
		t.Errorf("Content.RawText = %q, want %q", got.Content.RawText, s.Content.RawText)
	}
	if got.Source.Identity != s.Source.Identity {
		t.Errorf("Source.Identity = %q, want %q", got.Source.Identity, s.Source.Identity)
	}
	if !got.ReceivedAt.Equal(s.ReceivedAt) {
		t.Errorf("ReceivedAt = %v, want %v", got.ReceivedAt, s.ReceivedAt)
	}
	if got.OccurredAt == nil || !got.OccurredAt.Equal(*s.OccurredAt) {
		t.Errorf("OccurredAt = %v, want %v", got.OccurredAt, s.OccurredAt)
	}
	if len(got.Hints.Tags) != 2 || got.Hints.Tags[0] != "error" || got.Hints.Tags[1] != "folder-watcher" {
		t.Errorf("Hints.Tags = %v, want [error folder-watcher]", got.Hints.Tags)
	}
}

func TestStimulus_NilOccurredAt_OmittedFromJSON(t *testing.T) {
	s := common.Stimulus{
		StimulusID: "id-nil-occurred",
		ReceivedAt: time.Now().UTC(),
		Channel:    "test",
	}
	b, _ := json.Marshal(s)
	if string(b) == "" {
		t.Fatal("marshal returned empty")
	}
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if _, ok := m["occurred_at"]; ok {
		t.Error("occurred_at should be omitted when nil")
	}
}
