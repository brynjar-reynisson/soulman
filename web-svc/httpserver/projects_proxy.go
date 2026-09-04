// projects_proxy.go forwards /api/projects/** to projects-svc's main
// port, preserving method and body. Unlike proxyGet (proxy.go, GET-only,
// query-string-only), this generalized proxy is needed because the
// projects CRUD routes span GET/POST/PUT/DELETE with JSON request
// bodies and path parameters. See
// docs/superpowers/specs/2026-09-04-projects-tool-design.md's "Backend
// API & queue orchestration" section — the /notify endpoint is
// deliberately NOT proxied here at all; it's reached directly on
// projects-svc's loopback-only listener.
package httpserver

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// proxyProjects forwards the incoming request to
// cfg.ProjectsSvcURL+upstreamPath(r), preserving method and body, and
// streams the response back verbatim. A non-2xx/network-error upstream
// response becomes a 502, matching proxyGet's convention.
func (s *Server) proxyProjects(upstreamPath func(r *http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		url := s.cfg.ProjectsSvcURL + upstreamPath(r)

		req, err := http.NewRequestWithContext(ctx, r.Method, url, r.Body)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		req.Header.Set("Content-Type", "application/json")

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

func projectsPath(r *http.Request) string    { return "/projects" }
func projectByName(r *http.Request) string   { return "/projects/" + chi.URLParam(r, "name") }
func promptsPath(r *http.Request) string     { return "/prompts" }
func promptByID(r *http.Request) string      { return "/prompts/" + chi.URLParam(r, "id") }
