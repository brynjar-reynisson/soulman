package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"soulman/web-svc/auth"
)

// Config holds the values httpserver needs beyond the port and verifier —
// kept as its own struct (rather than depending on web-svc/config
// directly) so tests can construct it without going through config.Load.
type Config struct {
	CORSAllowedOrigin string
	PerceptionSvcURL  string
	MemorySvcURL      string
	ThinkingSvcURL    string
	ActionSvcURL      string
	ReportsRoot       string
}

type Server struct {
	port       string
	cfg        Config
	verifier   *auth.Verifier
	httpClient *http.Client
	router     chi.Router
}

func New(port string, cfg Config, verifier *auth.Verifier) *Server {
	s := &Server{
		port:       port,
		cfg:        cfg,
		verifier:   verifier,
		httpClient: &http.Client{Timeout: 5 * time.Second},
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
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{s.cfg.CORSAllowedOrigin},
		AllowedMethods: []string{"GET", "OPTIONS"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
		MaxAge:         300,
	}))

	r.Get("/health", s.health)

	r.Group(func(r chi.Router) {
		r.Use(s.verifier.Middleware)
		r.Get("/api/status", s.apiStatus)
		r.Get("/api/episodes", s.proxyGet(s.cfg.MemorySvcURL, "/memory/episodes"))
		r.Get("/api/raw-inputs/recent", s.proxyGet(s.cfg.MemorySvcURL, "/raw-inputs/recent"))
		r.Get("/api/reports/latest", s.reportsLatest)
		r.Get("/api/reports", s.reportsByDate)
	})

	return r
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
