package client_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"soulman/cli/client"
)

func TestSend_Success_ReturnsStimulusID(t *testing.T) {
	var gotBody client.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/perceive/cli" {
			t.Errorf("path = %q, want /api/perceive/cli", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"stimulus_id": "stim-123"})
	}))
	defer srv.Close()

	id, err := client.Send(srv.URL, client.Request{Text: "hello", Mode: "note", Priority: "normal"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "stim-123" {
		t.Errorf("id = %q, want stim-123", id)
	}
	if gotBody.Text != "hello" || gotBody.Mode != "note" || gotBody.Priority != "normal" {
		t.Errorf("request body = %+v, want {hello note normal}", gotBody)
	}
}

func TestSend_ServerError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "text is required"})
	}))
	defer srv.Close()

	_, err := client.Send(srv.URL, client.Request{Text: "", Mode: "note", Priority: "normal"})
	if err == nil {
		t.Fatal("expected an error for a 400 response")
	}
}

func TestSend_ServerUnreachable_ReturnsError(t *testing.T) {
	_, err := client.Send("http://127.0.0.1:1", client.Request{Text: "hi", Mode: "stimulus", Priority: "normal"})
	if err == nil {
		t.Fatal("expected an error for an unreachable server")
	}
}

func TestSend_Non202WithNonJSONBody_ReturnsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		// Return plain HTML body (e.g., from a proxy or panic recovery), not JSON
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body>Internal Server Error</body></html>"))
	}))
	defer srv.Close()

	_, err := client.Send(srv.URL, client.Request{Text: "test", Mode: "note", Priority: "normal"})
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
	// Error should mention the HTTP status, not JSON decode failure
	errMsg := err.Error()
	if !strings.Contains(errMsg, "500") {
		t.Errorf("error message should mention status 500, got: %q", errMsg)
	}
	if strings.Contains(errMsg, "invalid character") {
		t.Errorf("error message should not mention JSON decode failure, got: %q", errMsg)
	}
}

func TestSendRaw_PostsFileBytesAndReturnsStimulusID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/perceive/raw" {
			t.Errorf("path = %q, want /api/perceive/raw", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"channel":"gmail","content":{"raw_text":"hi","content_type":"text"}}` {
			t.Errorf("body = %s, want the raw file bytes unchanged", body)
		}
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"stimulus_id": "injected-id-1"})
	}))
	defer srv.Close()

	id, err := client.SendRaw(srv.URL, []byte(`{"channel":"gmail","content":{"raw_text":"hi","content_type":"text"}}`))
	if err != nil {
		t.Fatalf("SendRaw: %v", err)
	}
	if id != "injected-id-1" {
		t.Errorf("id = %q, want injected-id-1", id)
	}
}

func TestSendRaw_ServerError_ReturnsErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "channel is required"})
	}))
	defer srv.Close()

	_, err := client.SendRaw(srv.URL, []byte(`{}`))
	if err == nil {
		t.Fatal("SendRaw: want error, got nil")
	}
}
