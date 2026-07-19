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

type fakeRawPublisher struct {
	published []*common.Stimulus
	err       error
}

func (f *fakeRawPublisher) Publish(_ context.Context, s *common.Stimulus) error {
	if f.err != nil {
		return f.err
	}
	f.published = append(f.published, s)
	return nil
}

func TestPerceiveRaw_FullStimulus_PublishedVerbatim(t *testing.T) {
	pub := &fakeRawPublisher{}
	srv := httpserver.NewWithPublisher(pub)

	body := `{
		"channel": "gmail",
		"source": {"identity": "test@example.com", "authenticated": false, "auth_method": "none"},
		"content": {"raw_text": "hello", "content_type": "text"},
		"hints": {"priority": "high"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/perceive/raw", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body: %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %d stimuli, want 1", len(pub.published))
	}
	got := pub.published[0]
	if got.Channel != "gmail" {
		t.Errorf("Channel = %q, want gmail", got.Channel)
	}
	if got.Source.Identity != "test@example.com" {
		t.Errorf("Source.Identity = %q, want test@example.com", got.Source.Identity)
	}
	if got.Content.RawText != "hello" {
		t.Errorf("Content.RawText = %q, want hello", got.Content.RawText)
	}
	if got.Hints.Priority != "high" {
		t.Errorf("Hints.Priority = %q, want high", got.Hints.Priority)
	}
	if got.StimulusID == "" {
		t.Error("StimulusID = \"\", want a generated UUID since none was supplied")
	}
	if got.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", got.SchemaVersion)
	}
	if got.ReceivedAt.IsZero() {
		t.Error("ReceivedAt is zero, want a generated timestamp since none was supplied")
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["stimulus_id"] != got.StimulusID {
		t.Errorf("response stimulus_id = %q, want %q", resp["stimulus_id"], got.StimulusID)
	}
}

func TestPerceiveRaw_ExplicitStimulusID_Preserved(t *testing.T) {
	pub := &fakeRawPublisher{}
	srv := httpserver.NewWithPublisher(pub)

	body := `{"stimulus_id": "custom-id-123", "channel": "gmail", "content": {"raw_text": "x", "content_type": "text"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/perceive/raw", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body: %s", rec.Code, rec.Body.String())
	}
	if pub.published[0].StimulusID != "custom-id-123" {
		t.Errorf("StimulusID = %q, want custom-id-123 (explicit value must be preserved, not overwritten)", pub.published[0].StimulusID)
	}
}

func TestPerceiveRaw_MissingChannel_Returns400(t *testing.T) {
	pub := &fakeRawPublisher{}
	srv := httpserver.NewWithPublisher(pub)

	body := `{"content": {"raw_text": "x", "content_type": "text"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/perceive/raw", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(pub.published) != 0 {
		t.Error("expected nothing to be published for a request missing channel")
	}
}

func TestPerceiveRaw_InvalidJSON_Returns400(t *testing.T) {
	pub := &fakeRawPublisher{}
	srv := httpserver.NewWithPublisher(pub)

	req := httptest.NewRequest(http.MethodPost, "/api/perceive/raw", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPerceiveRaw_PublishFails_Returns503(t *testing.T) {
	pub := &fakeRawPublisher{err: errPublishBoom}
	srv := httpserver.NewWithPublisher(pub)

	body := `{"channel": "gmail", "content": {"raw_text": "x", "content_type": "text"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/perceive/raw", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

var errPublishBoom = errPublish{}

type errPublish struct{}

func (errPublish) Error() string { return "boom" }

func TestPerceiveRaw_MissingOccurredAt_DefaultsToReceivedAt(t *testing.T) {
	pub := &fakeRawPublisher{}
	srv := httpserver.NewWithPublisher(pub)

	body := `{"channel": "cli-note", "content": {"raw_text": "x", "content_type": "text"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/perceive/raw", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body: %s", rec.Code, rec.Body.String())
	}
	got := pub.published[0]
	if got.OccurredAt == nil {
		t.Fatal("OccurredAt is nil, want defaulted to ReceivedAt")
	}
	if !got.OccurredAt.Equal(got.ReceivedAt) {
		t.Errorf("OccurredAt = %v, want equal to ReceivedAt %v", got.OccurredAt, got.ReceivedAt)
	}
}
