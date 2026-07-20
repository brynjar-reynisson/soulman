package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/common"
	"soulman/perception-svc/httpserver"
)

type fakePublisher struct {
	published *common.Stimulus
	err       error
}

func (f *fakePublisher) Publish(_ context.Context, s *common.Stimulus) error {
	f.published = s
	return f.err
}

func postCLI(t *testing.T, srv *httpserver.Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/perceive/cli", bytes.NewBufferString(body))
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestPerceiveCLI_DefaultMode_PublishesCLIChannel(t *testing.T) {
	pub := &fakePublisher{}
	srv := httpserver.New("9001", nil, nil, pub, nil)

	rec := postCLI(t, srv, `{"text":"remind me to check logs"}`)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rec.Code, rec.Body.String())
	}
	if pub.published == nil {
		t.Fatal("expected Publish to be called")
	}
	if pub.published.Channel != "cli" {
		t.Errorf("Channel = %q, want cli", pub.published.Channel)
	}
	if pub.published.Content.RawText != "remind me to check logs" {
		t.Errorf("RawText = %q, want the request text", pub.published.Content.RawText)
	}
	if pub.published.Hints.Priority != "normal" {
		t.Errorf("Priority = %q, want normal (default)", pub.published.Hints.Priority)
	}
	if pub.published.Source.Identity != "cli" || !pub.published.Source.Authenticated || pub.published.Source.AuthMethod != "system" {
		t.Errorf("Source = %+v, want {cli, true, system}", pub.published.Source)
	}
	if pub.published.Override.IsOverride {
		t.Error("IsOverride = true, want false")
	}

	var respBody map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &respBody); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if respBody["stimulus_id"] == "" {
		t.Error("expected non-empty stimulus_id in response")
	}
	if respBody["stimulus_id"] != pub.published.StimulusID {
		t.Errorf("response stimulus_id = %q, want match with published %q", respBody["stimulus_id"], pub.published.StimulusID)
	}
}

func TestPerceiveCLI_NoteMode_PublishesCLINoteChannel(t *testing.T) {
	pub := &fakePublisher{}
	srv := httpserver.New("9001", nil, nil, pub, nil)

	rec := postCLI(t, srv, `{"text":"disk cleanup done","mode":"note","priority":"high"}`)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rec.Code, rec.Body.String())
	}
	if pub.published.Channel != "cli-note" {
		t.Errorf("Channel = %q, want cli-note", pub.published.Channel)
	}
	if pub.published.Hints.Priority != "high" {
		t.Errorf("Priority = %q, want high", pub.published.Hints.Priority)
	}
}

func TestPerceiveCLI_MissingText_Returns400(t *testing.T) {
	srv := httpserver.New("9001", nil, nil, &fakePublisher{}, nil)

	rec := postCLI(t, srv, `{"mode":"note"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPerceiveCLI_WhitespaceOnlyText_Returns400(t *testing.T) {
	srv := httpserver.New("9001", nil, nil, &fakePublisher{}, nil)

	rec := postCLI(t, srv, `{"text":"   ","mode":"note"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPerceiveCLI_InvalidMode_Returns400(t *testing.T) {
	srv := httpserver.New("9001", nil, nil, &fakePublisher{}, nil)

	rec := postCLI(t, srv, `{"text":"hi","mode":"bogus"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPerceiveCLI_InvalidPriority_Returns400(t *testing.T) {
	srv := httpserver.New("9001", nil, nil, &fakePublisher{}, nil)

	rec := postCLI(t, srv, `{"text":"hi","priority":"urgent"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPerceiveCLI_MalformedJSON_Returns400(t *testing.T) {
	srv := httpserver.New("9001", nil, nil, &fakePublisher{}, nil)

	rec := postCLI(t, srv, `{not json`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPerceiveCLI_PublishFails_Returns503(t *testing.T) {
	pub := &fakePublisher{err: context.DeadlineExceeded}
	srv := httpserver.New("9001", nil, nil, pub, nil)

	rec := postCLI(t, srv, `{"text":"hi"}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
