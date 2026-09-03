// Package calendar sends Google Calendar invites for school-event
// reminders. OAuth2 bootstrap mirrors
// perception-svc/gmailwatcher/client.go's constructor exactly (offline
// refresh token, golang.org/x/oauth2/google, Production app status), but
// against the Calendar API with the calendar.events scope instead of
// Gmail's. See
// docs/superpowers/specs/2026-09-03-school-email-events-design.md.
package calendar

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gcal "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

const calendarEventsScope = "https://www.googleapis.com/auth/calendar.events"

// Invite is one reminder to send as a Calendar event. No end time is ever
// supplied by the source email — timed events get a fixed 1-hour duration.
type Invite struct {
	Summary     string
	Description string
	Date        string // "YYYY-MM-DD"
	HasTime     bool
	Time        string // "HH:MM" local, only meaningful when HasTime
	Attendees   []string
}

type Client struct {
	svc *gcal.Service
}

// New builds an OAuth2 token source from clientID/clientSecret and a
// long-lived refresh token, then constructs a Calendar API client using
// it — same silent-refresh, no-interactive-reconsent shape as
// gmailwatcher.newRealGmailClient.
func New(ctx context.Context, clientID, clientSecret, refreshToken string) (*Client, error) {
	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{calendarEventsScope},
	}
	tokenSource := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	httpClient := oauth2.NewClient(ctx, tokenSource)

	svc, err := gcal.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("calendar: build calendar service: %w", err)
	}
	return &Client{svc: svc}, nil
}

// CreateInvite creates a primary-calendar event with sendUpdates="all" so
// every attendee gets a real invite email/notification.
func (c *Client) CreateInvite(ctx context.Context, inv Invite) error {
	ev := toCalendarEvent(inv)
	_, err := c.svc.Events.Insert("primary", ev).SendUpdates("all").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("calendar: create event %q: %w", inv.Summary, err)
	}
	return nil
}

// toCalendarEvent maps an Invite to the Calendar API's Event shape. Pure
// function — no network calls — so it's testable without a live account,
// mirroring gmailwatcher/stimulus.go's buildStimulus split. Date-only
// events use Start.Date/End.Date (all-day, end exclusive = Date+1 day) per
// the Calendar API's own convention. Timed events use
// Start.DateTime/End.DateTime with a fixed 1-hour duration.
func toCalendarEvent(inv Invite) *gcal.Event {
	attendees := make([]*gcal.EventAttendee, len(inv.Attendees))
	for i, email := range inv.Attendees {
		attendees[i] = &gcal.EventAttendee{Email: email}
	}

	ev := &gcal.Event{
		Summary:     inv.Summary,
		Description: inv.Description,
		Attendees:   attendees,
	}

	if !inv.HasTime {
		date, err := time.Parse("2006-01-02", inv.Date)
		if err != nil {
			date = time.Now()
		}
		ev.Start = &gcal.EventDateTime{Date: date.Format("2006-01-02")}
		ev.End = &gcal.EventDateTime{Date: date.AddDate(0, 0, 1).Format("2006-01-02")}
		return ev
	}

	start, err := time.ParseInLocation("2006-01-02 15:04", inv.Date+" "+inv.Time, time.Local)
	if err != nil {
		start = time.Now()
	}
	end := start.Add(1 * time.Hour)
	ev.Start = &gcal.EventDateTime{DateTime: start.Format(time.RFC3339)}
	ev.End = &gcal.EventDateTime{DateTime: end.Format(time.RFC3339)}
	return ev
}
