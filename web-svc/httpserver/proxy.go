package httpserver

import (
	"context"
	"io"
	"net/http"
	"time"
)

// proxyGet forwards the incoming request's query string to
// upstreamBaseURL+upstreamPath and streams the response back verbatim. A
// non-2xx/network-error upstream response becomes a 502 from web-svc
// rather than a hang or a crash — the frontend's per-panel error handling
// depends on getting a clear status code back quickly.
func (s *Server) proxyGet(upstreamBaseURL, upstreamPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		url := upstreamBaseURL + upstreamPath
		if r.URL.RawQuery != "" {
			url += "?" + r.URL.RawQuery
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}

		resp, err := s.httpClient.Do(req)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "upstream unavailable")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 500 {
			writeJSONError(w, http.StatusBadGateway, "upstream unavailable")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}
