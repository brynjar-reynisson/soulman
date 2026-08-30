package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"soulman/web-svc/websearch"
)

// searchTimeout bounds how long a single Brave Search API call may take,
// derived from the incoming request's context the same way isHealthy
// bounds its own outbound calls.
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
		slog.Error("web search failed", "error", err)
		writeJSONError(w, http.StatusBadGateway, "web search failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string][]websearch.Result{"results": results})
}
