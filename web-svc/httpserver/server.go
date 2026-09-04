package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"soulman/web-svc/auth"
	"soulman/web-svc/claudesession"
	"soulman/web-svc/filebrowser"
	"soulman/web-svc/websearch"
)

// Config holds the values httpserver needs beyond the port and verifier —
// kept as its own struct (rather than depending on web-svc/config
// directly) so tests can construct it without going through config.Load.
type Config struct {
	CORSAllowedOrigin  string
	PerceptionSvcURL   string
	MemorySvcURL       string
	ThinkingSvcURL     string
	ActionSvcURL       string
	ProjectsSvcURL     string
	ReportsRoot        string
	ObsidianRoot       string
	ClaudeProjectRoots []claudesession.Root
	FileBrowserRoots   []filebrowser.Root
	ShareLinkSecret    []byte
	ShareLinkTTL       time.Duration
	BraveSearchAPIKey  string
	// BraveSearchBaseURL overrides websearch.DefaultBaseURL when non-empty.
	// Production leaves this blank; tests set it to an httptest.Server URL.
	BraveSearchBaseURL string
}

type Server struct {
	port         string
	cfg          Config
	verifier     *auth.Verifier
	httpClient   *http.Client
	searchClient *websearch.Client
	router       chi.Router
}

func New(port string, cfg Config, verifier *auth.Verifier) *Server {
	baseURL := cfg.BraveSearchBaseURL
	if baseURL == "" {
		baseURL = websearch.DefaultBaseURL
	}
	s := &Server{
		port:         port,
		cfg:          cfg,
		verifier:     verifier,
		httpClient:   &http.Client{Timeout: 5 * time.Second},
		searchClient: websearch.NewClient(cfg.BraveSearchAPIKey, baseURL),
	}
	s.router = s.buildRouter()
	return s
}

func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) Start() error {
	return http.ListenAndServe(":"+s.port, s.router)
}

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(requestLogger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{s.cfg.CORSAllowedOrigin},
		AllowedMethods: []string{"GET", "POST", "PUT", "OPTIONS"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
		MaxAge:         300,
	}))

	r.Get("/health", s.health)

	r.Group(func(r chi.Router) {
		r.Use(s.verifier.Middleware)
		r.Get("/api/status", s.apiStatus)
		r.Get("/api/episodes", s.proxyGet(s.cfg.MemorySvcURL, "/memory/episodes"))
		r.Get("/api/raw-inputs/recent", s.proxyGet(s.cfg.MemorySvcURL, "/raw-inputs/recent"))
		r.Get("/api/system-monitor", s.proxyGet(s.cfg.PerceptionSvcURL, "/api/system-monitor/status"))
		r.Get("/api/reports/latest", s.reportsLatest)
		r.Get("/api/reports", s.reportsByDate)
		r.Get("/api/obsidian/folders", s.obsidianFolders)
		r.Get("/api/obsidian/files", s.obsidianFiles)
		r.Get("/api/obsidian/file", s.obsidianFileGet)
		r.Put("/api/obsidian/file", s.obsidianFilePut)
		r.Post("/api/obsidian/file", s.obsidianFilePost)
		r.Post("/api/obsidian/file/rename", s.obsidianFileRename)
		r.Get("/api/claude/roots", s.claudeRoots)
		r.Post("/api/claude/launch", s.claudeLaunch)
		r.Get("/api/files/roots", s.filesRoots)
		r.Get("/api/files/list", s.filesList)
		r.Get("/api/files/download", s.filesDownload)
		r.Post("/api/files/upload", s.filesUpload)
		r.Post("/api/files/share", s.filesShare)
		r.Get("/api/search", s.search)
		r.Get("/api/projects/projects", s.proxyProjects(projectsPath))
		r.Post("/api/projects/projects", s.proxyProjects(projectsPath))
		r.Put("/api/projects/projects/{name}", s.proxyProjects(projectByName))
		r.Delete("/api/projects/projects/{name}", s.proxyProjects(projectByName))
		r.Get("/api/projects/prompts", s.proxyProjects(promptsPath))
		r.Post("/api/projects/prompts", s.proxyProjects(promptsPath))
		r.Put("/api/projects/prompts/{id}", s.proxyProjects(promptByID))
	})

	r.Get("/dl/{token}", s.shareDownload)

	return r
}

// shareDownloadPrefix is the URL prefix of the one route in this service
// whose path is itself a credential — see requestLogger.
const shareDownloadPrefix = "/dl/"

// redactedSharePath replaces a share link's real path in the request log.
const redactedSharePath = "/dl/<redacted>"

// searchPath is the search route, whose query string carries the user's
// search text — see requestLogger.
const searchPath = "/api/search"

// redactedSearchPath replaces a search request's query string in the
// request log.
const redactedSearchPath = "/api/search?q=<redacted>"

// requestLogger logs every request, but routes two kinds of request away
// from chi's middleware.Logger: share-link downloads (a /dl/{token} URL *is*
// the bearer capability — no other credential is needed to fetch the file)
// and searches (whose query string is the user's search text). Both would
// otherwise be written verbatim into web-svc-startup.log by
// middleware.Logger, which is never rotated. Everything else keeps chi's
// logger unchanged.
//
// The split has to happen here, ahead of chi's logger, because
// middleware.RequestLogger snapshots the request path into its log entry
// *before* calling the next handler — there is no later hook that can
// change what it prints. Redacting by mutating r.URL.Path/r.URL.RawQuery in
// a preceding middleware isn't an option either: chi routes on the mutated
// path, so shareDownload's chi.URLParam(r, "token") would receive the
// placeholder instead of the real token.
func requestLogger(next http.Handler) http.Handler {
	chiLogged := middleware.Logger(next)
	shareLogged := shareDownloadLogger(next)
	searchLogged := searchQueryLogger(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, shareDownloadPrefix):
			shareLogged.ServeHTTP(w, r)
		case r.URL.Path == searchPath:
			searchLogged.ServeHTTP(w, r)
		default:
			chiLogged.ServeHTTP(w, r)
		}
	})
}

// shareDownloadLogger logs method, status, response size and duration for a
// share-link download against a redacted path, via log/slog (this repo's
// standard logger) rather than chi's stdlib-log formatter.
func shareDownloadLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		defer func() {
			slog.Info("share link request",
				"method", r.Method,
				"path", redactedSharePath,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration", time.Since(start).String(),
			)
		}()
		next.ServeHTTP(ww, r)
	})
}

// searchQueryLogger logs method, status, response size and duration for a
// search request against a redacted path, the same shape as
// shareDownloadLogger — the query string carries the user's search text and
// this file's web-svc-startup.log is never rotated.
func searchQueryLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		defer func() {
			slog.Info("search request",
				"method", r.Method,
				"path", redactedSearchPath,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration", time.Since(start).String(),
			)
		}()
		next.ServeHTTP(ww, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	checks := map[string]string{
		"perception-svc": s.cfg.PerceptionSvcURL,
		"memory-svc":     s.cfg.MemorySvcURL,
		"thinking-svc":   s.cfg.ThinkingSvcURL,
		"action-svc":     s.cfg.ActionSvcURL,
	}

	result := make(map[string]string, len(checks))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, url := range checks {
		wg.Add(1)
		go func(name, url string) {
			defer wg.Done()
			status := "down"
			if s.isHealthy(url) {
				status = "up"
			}
			mu.Lock()
			result[name] = status
			mu.Unlock()
		}(name, url)
	}
	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) isHealthy(baseURL string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
