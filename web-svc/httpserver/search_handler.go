package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"soulman/web-svc/websearch"
)

// searchTimeout bounds how long a single Brave Search API call may take.
// It's applied to a context derived from the incoming request's context
// (r.Context()), unlike isHealthy (web-svc/httpserver/server.go), which
// derives its timeout from context.Background() rather than a request
// context.
const searchTimeout = 5 * time.Second

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeJSONError(w, http.StatusBadRequest, "q is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
	defer cancel()

	results, err := s.searchClient.Search(ctx, query)
	if err != nil {
		if errors.Is(err, websearch.ErrNoAPIKey) {
			writeJSONError(w, http.StatusServiceUnavailable, "web search is not configured")
			return
		}
		// Deliberately not logging err.Error() here: a transport-level
		// failure (timeout, DNS, connection refused) wraps a *url.Error that
		// embeds the full outbound Brave URL, including the user's search
		// text in its query string — the same value this file's other
		// redaction (see requestLogger in server.go) exists to keep out of
		// web-svc-startup.log, which is never rotated.
		slog.Error("web search failed")
		writeJSONError(w, http.StatusBadGateway, "web search failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string][]websearch.Result{"results": results})
}
