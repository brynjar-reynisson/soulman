package httpserver

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"soulman/memory-svc/storage"
)

type Server struct {
	db     *storage.DB
	port   string
	router chi.Router
}

func New(db *storage.DB, port string) *Server {
	s := &Server{db: db, port: port}
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

	r.Get("/health", s.health)
	r.Get("/raw-inputs/recent", s.rawInputsRecent)
	r.Get("/memory/search", stub)
	r.Get("/memory/episodes", s.memoryEpisodes)
	r.Get("/memory/procedures", stub)
	r.Get("/memory/goals", stub)

	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	body := map[string]string{"status": "ok"}
	if s.db == nil {
		body["db"] = "unavailable"
	} else {
		body["db"] = "connected"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

func (s *Server) rawInputsRecent(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	rows, err := s.db.GetRecent(r.Context(), limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if rows == nil {
		rows = []storage.RawInput{}
	}
	json.NewEncoder(w).Encode(rows)
}

func (s *Server) memoryEpisodes(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	rows, err := s.db.GetRecentEpisodes(r.Context(), limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if rows == nil {
		rows = []storage.Episode{}
	}
	json.NewEncoder(w).Encode(rows)
}

func stub(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Not Implemented", http.StatusNotImplemented)
}
