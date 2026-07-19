package gmailwatcher

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/gmail/v1"
)

func encodeBody(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func TestBuildStimulus_PlainTextMessage(t *testing.T) {
	msg := &gmail.Message{
		Id:           "msg-1",
		ThreadId:     "thread-1",
		LabelIds:     []string{"INBOX", "UNREAD"},
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "text/plain",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: `"Jane Doe" <jane@example.com>`},
				{Name: "Subject", Value: "Server is down"},
			},
			Body: &gmail.MessagePartBody{Data: encodeBody("Everything is on fire.")},
		},
	}

	s, err := buildStimulus(msg)
	if err != nil {
		t.Fatalf("buildStimulus: %v", err)
	}

	if s.Channel != "gmail" {
		t.Errorf("Channel = %q, want gmail", s.Channel)
	}
	if s.Source.Identity != "jane@example.com" {
		t.Errorf("Source.Identity = %q, want jane@example.com", s.Source.Identity)
	}
	if s.Source.Authenticated {
		t.Error("Source.Authenticated = true, want false (sender identity unverified)")
	}
	if s.Source.AuthMethod != "none" {
		t.Errorf("Source.AuthMethod = %q, want none", s.Source.AuthMethod)
	}
	if s.Content.RawText != "Everything is on fire." {
		t.Errorf("Content.RawText = %q, want %q", s.Content.RawText, "Everything is on fire.")
	}
	if s.Content.ContentType != "text" {
		t.Errorf("Content.ContentType = %q, want text", s.Content.ContentType)
	}
	if len(s.Content.Attachments) != 0 {
		t.Errorf("Content.Attachments = %v, want empty", s.Content.Attachments)
	}
	if s.ChannelMeta.MessageID != "msg-1" {
		t.Errorf("ChannelMeta.MessageID = %q, want msg-1", s.ChannelMeta.MessageID)
	}
	if s.ChannelMeta.ThreadID != "thread-1" {
		t.Errorf("ChannelMeta.ThreadID = %q, want thread-1", s.ChannelMeta.ThreadID)
	}
	if s.ChannelMeta.ReplyTo != "jane@example.com" {
		t.Errorf("ChannelMeta.ReplyTo = %q, want jane@example.com", s.ChannelMeta.ReplyTo)
	}
	var channelSpecific struct {
		Subject  string   `json:"subject"`
		LabelIDs []string `json:"label_ids"`
	}
	if err := json.Unmarshal(s.ChannelMeta.ChannelSpecific, &channelSpecific); err != nil {
		t.Fatalf("unmarshal channel_specific: %v", err)
	}
	if channelSpecific.Subject != "Server is down" {
		t.Errorf("channel_specific.subject = %q, want %q", channelSpecific.Subject, "Server is down")
	}
	if len(channelSpecific.LabelIDs) != 2 {
		t.Errorf("channel_specific.label_ids = %v, want 2 entries", channelSpecific.LabelIDs)
	}
	wantOccurred := time.UnixMilli(1700000000000).UTC()
	if s.OccurredAt == nil || !s.OccurredAt.Equal(wantOccurred) {
		t.Errorf("OccurredAt = %v, want %v", s.OccurredAt, wantOccurred)
	}
	if s.Hints.Priority != "normal" {
		t.Errorf("Hints.Priority = %q, want normal", s.Hints.Priority)
	}
	if len(s.Hints.Tags) != 2 || s.Hints.Tags[0] != "email" || s.Hints.Tags[1] != "gmail" {
		t.Errorf("Hints.Tags = %v, want [email gmail]", s.Hints.Tags)
	}
	if s.Override.IsOverride {
		t.Error("Override.IsOverride = true, want false")
	}
}

func TestBuildStimulus_MultipartWithHTMLFallbackAndAttachment(t *testing.T) {
	msg := &gmail.Message{
		Id:           "msg-2",
		ThreadId:     "thread-2",
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "multipart/mixed",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "noreply@example.com"},
				{Name: "Subject", Value: "Your invoice"},
			},
			Parts: []*gmail.MessagePart{
				{
					MimeType: "text/html",
					Body:     &gmail.MessagePartBody{Data: encodeBody("<p>Invoice attached</p>")},
				},
				{
					MimeType: "application/pdf",
					Filename: "invoice.pdf",
					Body: &gmail.MessagePartBody{
						AttachmentId: "att-1",
						Size:         54321,
					},
				},
			},
		},
	}

	s, err := buildStimulus(msg)
	if err != nil {
		t.Fatalf("buildStimulus: %v", err)
	}

	if s.Content.RawText != "<p>Invoice attached</p>" {
		t.Errorf("Content.RawText = %q, want %q", s.Content.RawText, "<p>Invoice attached</p>")
	}
	if s.Content.ContentType != "html" {
		t.Errorf("Content.ContentType = %q, want html", s.Content.ContentType)
	}
	if len(s.Content.Attachments) != 1 {
		t.Fatalf("Content.Attachments = %v, want 1 entry", s.Content.Attachments)
	}
	att := s.Content.Attachments[0]
	if att.Filename != "invoice.pdf" {
		t.Errorf("Attachment.Filename = %q, want invoice.pdf", att.Filename)
	}
	if att.SizeBytes != 54321 {
		t.Errorf("Attachment.SizeBytes = %d, want 54321", att.SizeBytes)
	}
	wantURI := "gmail://msg-2/attachments/att-1"
	if att.URI != wantURI {
		t.Errorf("Attachment.URI = %q, want %q", att.URI, wantURI)
	}
	if s.Source.Identity != "noreply@example.com" {
		t.Errorf("Source.Identity = %q, want noreply@example.com", s.Source.Identity)
	}
}

func TestBuildStimulus_PrefersPlainTextOverHTML(t *testing.T) {
	msg := &gmail.Message{
		Id:           "msg-3",
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "multipart/alternative",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "sender@example.com"},
			},
			Parts: []*gmail.MessagePart{
				{MimeType: "text/html", Body: &gmail.MessagePartBody{Data: encodeBody("<p>html body</p>")}},
				{MimeType: "text/plain", Body: &gmail.MessagePartBody{Data: encodeBody("plain body")}},
			},
		},
	}

	s, err := buildStimulus(msg)
	if err != nil {
		t.Fatalf("buildStimulus: %v", err)
	}
	if s.Content.RawText != "plain body" {
		t.Errorf("Content.RawText = %q, want plain body (text/plain preferred over text/html)", s.Content.RawText)
	}
	if s.Content.ContentType != "text" {
		t.Errorf("Content.ContentType = %q, want text", s.Content.ContentType)
	}
}

func TestBuildStimulus_MalformedFromHeader_FallsBackToRawValue(t *testing.T) {
	msg := &gmail.Message{
		Id:           "msg-4",
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "text/plain",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "not a valid address!!!"},
			},
			Body: &gmail.MessagePartBody{Data: encodeBody("body")},
		},
	}

	s, err := buildStimulus(msg)
	if err != nil {
		t.Fatalf("buildStimulus: %v", err)
	}
	if s.Source.Identity != "not a valid address!!!" {
		t.Errorf("Source.Identity = %q, want raw header fallback %q", s.Source.Identity, "not a valid address!!!")
	}
}

func TestBuildStimulus_NoPayload_ReturnsError(t *testing.T) {
	msg := &gmail.Message{Id: "msg-5"}

	_, err := buildStimulus(msg)
	if err == nil {
		t.Fatal("buildStimulus: want error for message with nil Payload, got nil")
	}
}

func TestBuildStimulus_NoTextBody_EmptyRawText(t *testing.T) {
	msg := &gmail.Message{
		Id:           "msg-6",
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "multipart/mixed",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "sender@example.com"},
			},
			Parts: []*gmail.MessagePart{
				{
					MimeType: "application/pdf",
					Filename: "report.pdf",
					Body:     &gmail.MessagePartBody{AttachmentId: "att-2", Size: 1024},
				},
			},
		},
	}

	s, err := buildStimulus(msg)
	if err != nil {
		t.Fatalf("buildStimulus: %v", err)
	}
	if s.Content.RawText != "" {
		t.Errorf("Content.RawText = %q, want empty for attachment-only message", s.Content.RawText)
	}
	if len(s.Content.Attachments) != 1 {
		t.Errorf("Content.Attachments = %v, want 1 entry", s.Content.Attachments)
	}
}

func TestBuildStimulus_InlinedAttachmentWithoutAttachmentId(t *testing.T) {
	// Small attachments come with Body.Data inlined and no AttachmentId.
	// This test verifies they are recognized as attachments (with empty URI)
	// and the body text is still extracted from the separate text/plain part.
	msg := &gmail.Message{
		Id:           "msg-7",
		ThreadId:     "thread-7",
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "multipart/mixed",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "alice@example.com"},
				{Name: "Subject", Value: "Document attached"},
			},
			Parts: []*gmail.MessagePart{
				{
					MimeType: "text/plain",
					Body:     &gmail.MessagePartBody{Data: encodeBody("Here is the document you requested.")},
				},
				{
					MimeType:   "application/octet-stream",
					Filename:   "data.bin",
					Body:       &gmail.MessagePartBody{Data: encodeBody("binary content here"), Size: 1234},
					// Note: AttachmentId is empty (inlined small attachment)
				},
			},
		},
	}

	s, err := buildStimulus(msg)
	if err != nil {
		t.Fatalf("buildStimulus: %v", err)
	}

	// Body text must come from the text/plain part, not from the attachment
	if s.Content.RawText != "Here is the document you requested." {
		t.Errorf("Content.RawText = %q, want %q", s.Content.RawText, "Here is the document you requested.")
	}

	// Attachment must be recognized despite missing AttachmentId
	if len(s.Content.Attachments) != 1 {
		t.Fatalf("Content.Attachments = %v, want 1 entry", s.Content.Attachments)
	}
	att := s.Content.Attachments[0]
	if att.Filename != "data.bin" {
		t.Errorf("Attachment.Filename = %q, want data.bin", att.Filename)
	}
	if att.MIMEType != "application/octet-stream" {
		t.Errorf("Attachment.MIMEType = %q, want application/octet-stream", att.MIMEType)
	}
	if att.SizeBytes != 1234 {
		t.Errorf("Attachment.SizeBytes = %d, want 1234", att.SizeBytes)
	}
	// For inlined attachments (no AttachmentId), URI must be empty
	if att.URI != "" {
		t.Errorf("Attachment.URI = %q, want empty string (no AttachmentId to fetch)", att.URI)
	}
}

func TestBuildStimulus_TextFileAttachmentNotMistakenForBody(t *testing.T) {
	// A part with MimeType text/plain and a Filename is an attachment,
	// never the message body. This prevents mistaking a .txt file for the email text.
	msg := &gmail.Message{
		Id:           "msg-8",
		ThreadId:     "thread-8",
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "text/plain",
			Filename: "notes.txt",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "bob@example.com"},
			},
			Body: &gmail.MessagePartBody{Data: encodeBody("This is a file attachment, not the message body.")},
		},
	}

	s, err := buildStimulus(msg)
	if err != nil {
		t.Fatalf("buildStimulus: %v", err)
	}

	// The text/plain part must NOT be treated as the message body
	// because it has a Filename (it's an attachment).
	if s.Content.RawText != "" {
		t.Errorf("Content.RawText = %q, want empty (text/plain part with Filename is attachment, not body)", s.Content.RawText)
	}

	// The text file must appear in attachments
	if len(s.Content.Attachments) != 1 {
		t.Fatalf("Content.Attachments = %v, want 1 entry", s.Content.Attachments)
	}
	att := s.Content.Attachments[0]
	if att.Filename != "notes.txt" {
		t.Errorf("Attachment.Filename = %q, want notes.txt", att.Filename)
	}
	if att.MIMEType != "text/plain" {
		t.Errorf("Attachment.MIMEType = %q, want text/plain", att.MIMEType)
	}
}

// TestBuildStimulus_PaddedBase64Body_DecodesCorrectly is a regression test:
// real Gmail API responses have been observed returning body.Data WITH
// trailing "=" padding, not the unpadded form the base64url spec's Gmail
// docs imply. base64.RawURLEncoding alone rejects the padding character
// with "illegal base64 data" — decodeBody must strip it first.
func TestBuildStimulus_PaddedBase64Body_DecodesCorrectly(t *testing.T) {
	paddedData := base64.URLEncoding.EncodeToString([]byte("padded body text"))
	if !strings.HasSuffix(paddedData, "=") {
		t.Fatalf("test setup: expected %q to end in padding for this test to be meaningful", paddedData)
	}

	msg := &gmail.Message{
		Id:           "msg-9",
		InternalDate: 1700000000000,
		Payload: &gmail.MessagePart{
			MimeType: "text/plain",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "sender@example.com"},
			},
			Body: &gmail.MessagePartBody{Data: paddedData},
		},
	}

	s, err := buildStimulus(msg)
	if err != nil {
		t.Fatalf("buildStimulus: %v", err)
	}
	if s.Content.RawText != "padded body text" {
		t.Errorf("Content.RawText = %q, want %q", s.Content.RawText, "padded body text")
	}
}
