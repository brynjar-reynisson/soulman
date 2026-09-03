package main

import (
	"testing"
	"time"

	"google.golang.org/api/gmail/v1"
)

func TestBuildBackfillStimulus_MapsCoreFields(t *testing.T) {
	msg := &gmail.Message{
		Id:           "msg-1",
		ThreadId:     "thread-1",
		InternalDate: 1756800000000, // 2025-09-02T08:00:00Z
		Payload: &gmail.MessagePart{
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "teacher@reykjavik.is"},
				{Name: "Subject", Value: "Sweater day reminder"},
			},
			MimeType: "text/plain",
			Body:     &gmail.MessagePartBody{Data: "SGVsbG8="}, // "Hello"
		},
	}

	s, err := buildBackfillStimulus(msg)
	if err != nil {
		t.Fatalf("buildBackfillStimulus: %v", err)
	}
	if s.Channel != "gmail" {
		t.Errorf("Channel = %q, want gmail", s.Channel)
	}
	if s.Source.Identity != "teacher@reykjavik.is" {
		t.Errorf("Source.Identity = %q, want teacher@reykjavik.is", s.Source.Identity)
	}
	if s.Content.RawText != "Hello" {
		t.Errorf("Content.RawText = %q, want Hello", s.Content.RawText)
	}
	if s.ChannelMeta.ThreadID != "thread-1" {
		t.Errorf("ChannelMeta.ThreadID = %q, want thread-1", s.ChannelMeta.ThreadID)
	}
	if s.OccurredAt == nil || !s.OccurredAt.Equal(time.UnixMilli(1756800000000).UTC()) {
		t.Errorf("OccurredAt = %v, want the message's real InternalDate (not time.Now)", s.OccurredAt)
	}
}

func TestBuildBackfillStimulus_NilPayload_Errors(t *testing.T) {
	msg := &gmail.Message{Id: "msg-2"}
	if _, err := buildBackfillStimulus(msg); err == nil {
		t.Error("expected an error for a message with no payload")
	}
}
