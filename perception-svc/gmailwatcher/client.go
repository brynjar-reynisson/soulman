package gmailwatcher

import (
	"context"
	"fmt"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

const (
	gmailReadonlyScope = "https://www.googleapis.com/auth/gmail.readonly"
	gmailModifyScope   = "https://www.googleapis.com/auth/gmail.modify"

	gmailUser = "me" // "me" always refers to the authenticated account in the Gmail API
)

// gmailClient is the seam between the poll loop and the real Gmail API —
// small and hand-rolled (not the full *gmail.Service surface) so
// gmailwatcher.go's orchestration logic is testable against a fake without
// a live Gmail account, mirroring how watcher.Publisher lets folder-watcher's
// tests avoid a real NATS server.
type gmailClient interface {
	// ListMatching returns the message IDs currently matching query.
	ListMatching(ctx context.Context, query string) ([]string, error)
	// GetMessage fetches the full message body/headers for id.
	GetMessage(ctx context.Context, id string) (*gmail.Message, error)
	// EnsureLabel resolves name to a label ID, creating the label if it
	// doesn't exist yet.
	EnsureLabel(ctx context.Context, name string) (string, error)
	// AddLabel applies labelID to message id.
	AddLabel(ctx context.Context, id, labelID string) error
}

// realGmailClient implements gmailClient against the live Gmail API.
type realGmailClient struct {
	svc *gmail.Service
}

// newRealGmailClient builds an OAuth2 token source from clientID/clientSecret
// and a long-lived refresh token, then constructs a Gmail API client using
// it. The token source refreshes the access token automatically and
// indefinitely — no interactive re-consent — per this channel's design
// (see docs/superpowers/specs/2026-07-18-gmail-channel-design.md's OAuth
// Setup section on why Production app status is what actually prevents
// the refresh token itself from expiring).
func newRealGmailClient(ctx context.Context, clientID, clientSecret, refreshToken string) (*realGmailClient, error) {
	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{gmailReadonlyScope, gmailModifyScope},
	}
	tokenSource := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	httpClient := oauth2.NewClient(ctx, tokenSource)

	svc, err := gmail.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("gmailwatcher: build gmail service: %w", err)
	}
	return &realGmailClient{svc: svc}, nil
}

func (c *realGmailClient) ListMatching(ctx context.Context, query string) ([]string, error) {
	var ids []string
	err := c.svc.Users.Messages.List(gmailUser).Q(query).Pages(ctx, func(resp *gmail.ListMessagesResponse) error {
		for _, m := range resp.Messages {
			ids = append(ids, m.Id)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("gmailwatcher: list messages: %w", err)
	}
	return ids, nil
}

func (c *realGmailClient) GetMessage(ctx context.Context, id string) (*gmail.Message, error) {
	msg, err := c.svc.Users.Messages.Get(gmailUser, id).Format("full").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gmailwatcher: get message %s: %w", id, err)
	}
	return msg, nil
}

// EnsureLabel looks up name among the account's existing labels; if not
// found, creates it. Gmail label names containing "/" (e.g.
// "soulman/seen-dev") are nested labels — Gmail creates the parent
// automatically, no special handling needed here.
func (c *realGmailClient) EnsureLabel(ctx context.Context, name string) (string, error) {
	resp, err := c.svc.Users.Labels.List(gmailUser).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("gmailwatcher: list labels: %w", err)
	}
	for _, l := range resp.Labels {
		if l.Name == name {
			return l.Id, nil
		}
	}

	created, err := c.svc.Users.Labels.Create(gmailUser, &gmail.Label{
		Name:                  name,
		LabelListVisibility:   "labelShow",
		MessageListVisibility: "show",
	}).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("gmailwatcher: create label %s: %w", name, err)
	}
	return created.Id, nil
}

func (c *realGmailClient) AddLabel(ctx context.Context, id, labelID string) error {
	_, err := c.svc.Users.Messages.Modify(gmailUser, id, &gmail.ModifyMessageRequest{
		AddLabelIds: []string{labelID},
	}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("gmailwatcher: add label %s to message %s: %w", labelID, id, err)
	}
	return nil
}
