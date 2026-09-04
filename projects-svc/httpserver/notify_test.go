package httpserver_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/projects-svc/httpserver"
	"soulman/projects-svc/store"
)

// newNoopDispatcher is defined in server_test.go (Task 6, same
// httpserver_test package) — reused here, not redefined.

type fakeNotifyStore struct {
	updateStateErr error
	gotID          int64
	gotState       string
}

func (f *fakeNotifyStore) UpdatePromptState(ctx context.Context, id int64, state string) error {
	f.gotID, f.gotState = id, state
	return f.updateStateErr
}

func TestNotify_LoopbackAddress_Implementing_Returns204(t *testing.T) {
	fs := &fakeNotifyStore{}
	srv := httpserver.NewNotifyServer(fs, newNoopDispatcher())

	body := bytes.NewReader([]byte(`{"prompt_id": 7, "state": "IMPLEMENTING"}`))
	req := httptest.NewRequest(http.MethodPost, "/notify", body)
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
	if fs.gotID != 7 || fs.gotState != store.StateImplementing {
		t.Errorf("UpdatePromptState called with (%d, %q), want (7, IMPLEMENTING)", fs.gotID, fs.gotState)
	}
}

func TestNotify_NonLoopbackAddress_Returns403(t *testing.T) {
	fs := &fakeNotifyStore{}
	srv := httpserver.NewNotifyServer(fs, newNoopDispatcher())

	body := bytes.NewReader([]byte(`{"prompt_id": 7, "state": "IMPLEMENTING"}`))
	req := httptest.NewRequest(http.MethodPost, "/notify", body)
	req.RemoteAddr = "203.0.113.5:54321" // TEST-NET-3, a real non-loopback address
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if fs.gotID != 0 {
		t.Error("UpdatePromptState should not be called for a rejected request")
	}
}

func TestNotify_InvalidState_Returns400(t *testing.T) {
	fs := &fakeNotifyStore{}
	srv := httpserver.NewNotifyServer(fs, newNoopDispatcher())

	body := bytes.NewReader([]byte(`{"prompt_id": 7, "state": "NOT_A_REAL_STATE"}`))
	req := httptest.NewRequest(http.MethodPost, "/notify", body)
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestNotify_NotFoundPromptID_Returns404(t *testing.T) {
	fs := &fakeNotifyStore{updateStateErr: store.ErrNotFound}
	srv := httpserver.NewNotifyServer(fs, newNoopDispatcher())

	body := bytes.NewReader([]byte(`{"prompt_id": 999, "state": "DONE"}`))
	req := httptest.NewRequest(http.MethodPost, "/notify", body)
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
