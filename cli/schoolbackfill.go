package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/mail"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"

	"soulman/cli/client"
	"soulman/common"
)

// runSchoolBackfill implements `soulman school-backfill --since YYYY-MM-DD
// [--dry-run]`: a one-off historical scan of @reykjavik.is mail,
// feeding each match through perception-svc's existing debug-injection
// endpoint (POST /api/perceive/raw) — reusing the live pipeline rather
// than building special-case backfill logic. See
// docs/superpowers/specs/2026-09-03-school-email-events-design.md.
//
// Deliberately duplicates gmailwatcher's OAuth bootstrap and MIME-body
// extraction rather than importing perception-svc directly — cli is a
// separate Go module, and this repo prefers small independent duplication
// over cross-module imports (see action-svc/NOTES.md).
func runSchoolBackfill(baseURL, since string, dryRun bool) error {
	clientID := os.Getenv("GMAIL_CLIENT_ID")
	clientSecret := os.Getenv("GMAIL_CLIENT_SECRET")
	refreshToken := os.Getenv("GMAIL_REFRESH_TOKEN")
	if clientID == "" || clientSecret == "" || refreshToken == "" {
		return fmt.Errorf("GMAIL_CLIENT_ID, GMAIL_CLIENT_SECRET, and GMAIL_REFRESH_TOKEN must all be set in the environment")
	}

	ctx := context.Background()
	svc, err := newGmailService(ctx, clientID, clientSecret, refreshToken)
	if err != nil {
		return fmt.Errorf("build gmail service: %w", err)
	}

	// Gmail search has no glob wildcard support - "from:*@domain" matches
	// nothing (no real address contains a literal "*"). "from:@domain" is
	// Gmail's actual supported syntax for "sender's address is at this
	// domain". Two domains: teachers sometimes send directly from
	// @reykjavik.is, but most school notifications relay through the
	// Mentor school-information platform (noreply@mentor.is) - matches
	// school.sender_domains in config/prod.json.
	query := fmt.Sprintf("(from:@reykjavik.is OR from:@mentor.is) after:%s", strings.ReplaceAll(since, "-", "/"))
	var ids []string
	if err := svc.Users.Messages.List("me").Q(query).Pages(ctx, func(resp *gmail.ListMessagesResponse) error {
		for _, m := range resp.Messages {
			ids = append(ids, m.Id)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("list messages: %w", err)
	}

	fmt.Printf("found %d message(s) matching %q\n", len(ids), query)

	injected := 0
	for _, id := range ids {
		msg, err := svc.Users.Messages.Get("me", id).Format("full").Context(ctx).Do()
		if err != nil {
			fmt.Printf("  %s: get failed: %v\n", id, err)
			continue
		}
		s, err := buildBackfillStimulus(msg)
		if err != nil {
			fmt.Printf("  %s: build stimulus failed: %v\n", id, err)
			continue
		}
		body, err := json.Marshal(s)
		if err != nil {
			fmt.Printf("  %s: marshal failed: %v\n", id, err)
			continue
		}
		if dryRun {
			fmt.Printf("  %s: [dry-run] would inject (occurred_at=%s)\n", id, s.OccurredAt.Format(time.RFC3339))
			continue
		}
		stimulusID, err := client.SendRaw(baseURL, body)
		if err != nil {
			fmt.Printf("  %s: inject failed: %v\n", id, err)
			continue
		}
		fmt.Printf("  %s: injected (stimulus_id: %s)\n", id, stimulusID)
		injected++
	}

	if !dryRun {
		fmt.Printf("injected %d/%d message(s)\n", injected, len(ids))
	}
	return nil
}

func newGmailService(ctx context.Context, clientID, clientSecret, refreshToken string) (*gmail.Service, error) {
	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{"https://www.googleapis.com/auth/gmail.readonly"},
	}
	tokenSource := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	httpClient := oauth2.NewClient(ctx, tokenSource)
	return gmail.NewService(ctx, option.WithHTTPClient(httpClient))
}

// buildBackfillStimulus maps a fully-fetched Gmail message into a
// common.Stimulus, setting OccurredAt from the message's real InternalDate
// (not time.Now) — critical so thinking-svc's extraction prompt resolves
// relative date phrases against when the email was actually sent. Only
// text/plain (falling back to text/html) is extracted; unlike
// gmailwatcher's full buildStimulus, attachment metadata is not collected
// here — not needed for extraction, and not worth the duplicated logic.
func buildBackfillStimulus(msg *gmail.Message) (*common.Stimulus, error) {
	if msg.Payload == nil {
		return nil, fmt.Errorf("message %s has no payload", msg.Id)
	}

	headers := map[string]string{}
	for _, h := range msg.Payload.Headers {
		headers[h.Name] = h.Value
	}
	fromAddr := headers["From"]
	if addr, err := mail.ParseAddress(fromAddr); err == nil {
		fromAddr = addr.Address
	}
	subject := headers["Subject"]

	rawText, contentType := extractBackfillBody(msg.Payload)
	occurredAt := time.UnixMilli(msg.InternalDate).UTC()

	channelSpecific, err := json.Marshal(map[string]string{"subject": subject})
	if err != nil {
		return nil, fmt.Errorf("marshal channel_specific: %w", err)
	}

	return &common.Stimulus{
		Channel:    "gmail",
		OccurredAt: &occurredAt,
		Source:     common.Source{Identity: fromAddr, AuthMethod: "none"},
		Content:    common.Content{RawText: rawText, ContentType: contentType},
		ChannelMeta: common.ChannelMeta{
			MessageID:       msg.Id,
			ThreadID:        msg.ThreadId,
			ChannelSpecific: json.RawMessage(channelSpecific),
		},
		Hints: common.Hints{Priority: "normal", Tags: []string{"email", "gmail", "backfill"}},
	}, nil
}

func extractBackfillBody(part *gmail.MessagePart) (text, contentType string) {
	plain, html := findBackfillTextParts(part)
	if plain != "" {
		return plain, "text"
	}
	if html != "" {
		return html, "html"
	}
	return "", "text"
}

func findBackfillTextParts(part *gmail.MessagePart) (plain, html string) {
	switch part.MimeType {
	case "text/plain":
		return decodeBackfillBody(part.Body), ""
	case "text/html":
		return "", decodeBackfillBody(part.Body)
	}
	for _, child := range part.Parts {
		p, h := findBackfillTextParts(child)
		if plain == "" {
			plain = p
		}
		if html == "" {
			html = h
		}
		if plain != "" {
			return plain, html
		}
	}
	return plain, html
}

func decodeBackfillBody(body *gmail.MessagePartBody) string {
	if body == nil || body.Data == "" {
		return ""
	}
	trimmed := strings.TrimRight(body.Data, "=")
	decoded, err := base64.RawURLEncoding.DecodeString(trimmed)
	if err != nil {
		return ""
	}
	return string(decoded)
}
