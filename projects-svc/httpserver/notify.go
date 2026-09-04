package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"

	"soulman/projects-svc/dispatch"
	"soulman/projects-svc/store"
)

// NotifyStore is the subset of *store.Store the notify listener calls.
type NotifyStore interface {
	UpdatePromptState(ctx context.Context, id int64, state string) error
}

// NewNotifyServer builds the loopback-only /notify listener as a
// standalone *http.Server, separate from the main CRUD router — projects-
// svc/main.go binds this one to "127.0.0.1:<NOTIFY_PORT>" specifically
// (not "0.0.0.0"), and the RemoteAddr check here is defense in depth on
// top of that binding. See
// docs/superpowers/specs/2026-09-04-projects-tool-design.md's "Backend
// API & queue orchestration" section.
func NewNotifyServer(st NotifyStore, d *dispatch.Dispatcher) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/notify", notifyHandler(st, d))
	return &http.Server{Handler: mux}
}

func notifyHandler(st NotifyStore, d *dispatch.Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.RemoteAddr) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}

		var body struct {
			PromptID int64  `json:"prompt_id"`
			State    string `json:"state"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if body.State != store.StateImplementing && body.State != store.StateDone {
			writeError(w, http.StatusBadRequest, "state must be IMPLEMENTING or DONE")
			return
		}

		if err := st.UpdatePromptState(r.Context(), body.PromptID, body.State); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "prompt not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		w.WriteHeader(http.StatusNoContent)

		if body.State == store.StateImplementing && d != nil {
			go dispatchNextSafely(d)
		}
	}
}

// dispatchNextSafely runs TryDispatchNext in the background goroutine
// notifyHandler spawns, recovering any panic so a bug in the dispatch
// path can never crash the whole process from an unrecovered goroutine
// panic — chi's middleware.Recoverer (used by the main CRUD router) does
// NOT protect goroutines spawned from within a handler, only the
// synchronous request-handling goroutine itself.
func dispatchNextSafely(d *dispatch.Dispatcher) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("notify: TryDispatchNext panicked", "recovered", r)
		}
	}()
	d.TryDispatchNext(context.Background())
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
