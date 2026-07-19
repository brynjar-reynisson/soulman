package httpserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"soulman/common"
)

// Publisher is satisfied by *natspublish.Publisher. Declared here (not
// imported from natspublish) to avoid an import cycle, mirroring the same
// pattern watcher.Publisher already uses.
type Publisher interface {
	Publish(ctx context.Context, s *common.Stimulus) error
}

type Server struct {
	port         string
	watchedPaths []string
	natsStatus   func() string
	publisher    Publisher
	router       chi.Router
}

func New(port string, watchedPaths []string, natsStatus func() string, publisher Publisher) *Server {
	s := &Server{port: port, watchedPaths: watchedPaths, natsStatus: natsStatus, publisher: publisher}
	s.router = s.buildRouter()
	return s
}

// NewWithPublisher builds a Server for tests that only exercise
// publisher-dependent handlers (like perceiveRaw) and don't care about
// port/watchedPaths/natsStatus.
func NewWithPublisher(publisher Publisher) *Server {
	return New("0", nil, nil, publisher)
}

func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) Start() error {
	return http.ListenAndServe(":"+s.port, s.router)
}

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Get("/health", s.health)
	r.Post("/api/perceive/cli", s.perceiveCLI)
	r.Post("/api/perceive/raw", s.perceiveRaw)
	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	status := "disconnected"
	if s.natsStatus != nil {
		status = s.natsStatus()
	}

	paths := s.watchedPaths
	if paths == nil {
		paths = []string{}
	}

	body := map[string]interface{}{
		"status":        "ok",
		"nats":          status,
		"watched_paths": paths,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}
