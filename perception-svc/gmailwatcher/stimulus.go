package gmailwatcher

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/api/gmail/v1"

	"soulman/common"
)

// buildStimulus maps a fully-fetched Gmail message (format "full") into the
// canonical Stimulus. Pure function — no network calls, no side effects —
// so it's testable against fixture messages without a live Gmail account.
func buildStimulus(msg *gmail.Message) (*common.Stimulus, error) {
	if msg.Payload == nil {
		return nil, fmt.Errorf("gmailwatcher: message %s has no payload", msg.Id)
	}

	headers := headerMap(msg.Payload.Headers)
	fromAddr := parseFromAddress(headers["From"])
	subject := headers["Subject"]

	rawText, contentType, err := extractBody(msg.Payload)
	if err != nil {
		return nil, fmt.Errorf("gmailwatcher: extract body for message %s: %w", msg.Id, err)
	}

	attachments := extractAttachments(msg.Id, msg.Payload)

	occurredAt := time.UnixMilli(msg.InternalDate).UTC()

	channelSpecific, err := json.Marshal(struct {
		Subject  string   `json:"subject"`
		LabelIDs []string `json:"label_ids"`
	}{Subject: subject, LabelIDs: msg.LabelIds})
	if err != nil {
		return nil, fmt.Errorf("gmailwatcher: marshal channel_specific for message %s: %w", msg.Id, err)
	}

	rawPayload, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("gmailwatcher: marshal raw_payload for message %s: %w", msg.Id, err)
	}

	id, err := uuid.NewV7()
	if err != nil {
		// Extremely unlikely (crypto/rand failure); fall back to a random v4
		// rather than drop the message, mirroring watcher.buildStimulus.
		id = uuid.New()
	}

	return &common.Stimulus{
		StimulusID:    id.String(),
		SchemaVersion: 1,
		ReceivedAt:    time.Now().UTC(),
		OccurredAt:    &occurredAt,
		Channel:       "gmail",
		Source: common.Source{
			Identity:      fromAddr,
			Authenticated: false,
			AuthMethod:    "none",
		},
		Content: common.Content{
			RawText:     rawText,
			RawPayload:  json.RawMessage(rawPayload),
			ContentType: contentType,
			Attachments: attachments,
		},
		ChannelMeta: common.ChannelMeta{
			MessageID:       msg.Id,
			ThreadID:        msg.ThreadId,
			ReplyTo:         fromAddr,
			ChannelSpecific: json.RawMessage(channelSpecific),
		},
		Hints: common.Hints{
			Priority: "normal",
			Tags:     []string{"email", "gmail"},
		},
		Override: common.Override{
			IsOverride: false,
			Params:     json.RawMessage(`{}`),
		},
	}, nil
}

func headerMap(headers []*gmail.MessagePartHeader) map[string]string {
	m := make(map[string]string, len(headers))
	for _, h := range headers {
		m[h.Name] = h.Value
	}
	return m
}

// parseFromAddress extracts just the email address from a raw "From"
// header value (e.g. `"Jane Doe" <jane@example.com>` -> "jane@example.com").
// Falls back to the raw header value if it doesn't parse as an RFC 5322
// address, rather than dropping the sender entirely.
func parseFromAddress(from string) string {
	if from == "" {
		return ""
	}
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return from
	}
	return addr.Address
}

// extractBody walks the MIME part tree, preferring the first text/plain
// part found; if none exists, falls back to the first text/html part.
// Returns ("", "text", nil) if the message has no text body at all (e.g.
// an attachment-only message).
func extractBody(part *gmail.MessagePart) (text, contentType string, err error) {
	plain, html, err := findTextParts(part)
	if err != nil {
		return "", "", err
	}
	if plain != "" {
		return plain, "text", nil
	}
	if html != "" {
		return html, "html", nil
	}
	return "", "text", nil
}

// findTextParts walks part depth-first, returning the first text/plain and
// first text/html bodies it finds (either may be empty). Container parts
// (e.g. multipart/mixed) have neither MIME type and just recurse into
// their children. Parts with a Filename are treated as attachments and
// never as message body, regardless of MIME type.
func findTextParts(part *gmail.MessagePart) (plain, html string, err error) {
	// Skip any part that has a filename (it's an attachment, not body)
	if part.Filename != "" {
		// Still recurse into children in case this is a container
		for _, child := range part.Parts {
			p, h, err := findTextParts(child)
			if err != nil {
				return "", "", err
			}
			if plain == "" {
				plain = p
			}
			if html == "" {
				html = h
			}
			if plain != "" {
				return plain, html, nil
			}
		}
		return plain, html, nil
	}

	switch part.MimeType {
	case "text/plain":
		decoded, decErr := decodeBody(part.Body)
		if decErr != nil {
			return "", "", decErr
		}
		return decoded, "", nil
	case "text/html":
		decoded, decErr := decodeBody(part.Body)
		if decErr != nil {
			return "", "", decErr
		}
		return "", decoded, nil
	}

	for _, child := range part.Parts {
		p, h, err := findTextParts(child)
		if err != nil {
			return "", "", err
		}
		if plain == "" {
			plain = p
		}
		if html == "" {
			html = h
		}
		if plain != "" {
			return plain, html, nil
		}
	}
	return plain, html, nil
}

// decodeBody decodes a MIME part body's base64url-encoded Data field.
// Gmail's API is inconsistent about padding in practice — some messages
// come back with trailing "=" padding, others without — so any trailing
// padding is stripped before decoding with RawURLEncoding (which expects
// none), rather than assuming one form or the other.
func decodeBody(body *gmail.MessagePartBody) (string, error) {
	if body == nil || body.Data == "" {
		return "", nil
	}
	trimmed := strings.TrimRight(body.Data, "=")
	decoded, err := base64.RawURLEncoding.DecodeString(trimmed)
	if err != nil {
		return "", fmt.Errorf("decode body: %w", err)
	}
	return string(decoded), nil
}

// extractAttachments collects metadata (never bytes) for every MIME part
// that names a file, per the approved design decision — a synthetic
// gmail:// URI lets a future consumer fetch the real bytes via the Gmail
// API without perception-svc downloading them now. For small attachments,
// Gmail inlines the bytes in Body.Data with no AttachmentId; for large
// attachments, AttachmentId is set and a separate fetch is needed. Both
// cases are recognized by Filename being present (and Body != nil).
func extractAttachments(messageID string, part *gmail.MessagePart) []common.Attachment {
	var out []common.Attachment
	var walk func(p *gmail.MessagePart)
	walk = func(p *gmail.MessagePart) {
		if p.Filename != "" && p.Body != nil {
			att := common.Attachment{
				Filename:  p.Filename,
				MIMEType:  p.MimeType,
				SizeBytes: p.Body.Size,
			}
			// Only set URI if there's an AttachmentId (large attachment needing
			// separate fetch); for inline data (no AttachmentId), leave URI empty.
			if p.Body.AttachmentId != "" {
				att.URI = fmt.Sprintf("gmail://%s/attachments/%s", messageID, p.Body.AttachmentId)
			}
			out = append(out, att)
		}
		for _, child := range p.Parts {
			walk(child)
		}
	}
	walk(part)
	if out == nil {
		out = []common.Attachment{}
	}
	return out
}
