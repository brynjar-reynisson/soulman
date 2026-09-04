// Package httpserver serves projects-svc's main-port CRUD API for
// projects and prompts. The separate loopback-only /notify listener
// (Task 7) is a distinct http.Server, not part of this router — see
// docs/superpowers/specs/2026-09-04-projects-tool-design.md's
// "Backend API & queue orchestration" section for why they're split.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"soulman/projects-svc/dispatch"
	"soulman/projects-svc/store"
)

// Store is the subset of *store.Store this package calls directly.
type Store interface {
	ListProjects(ctx context.Context) ([]store.Project, error)
	CreateProject(ctx context.Context, p store.Project) error
	UpdateProject(ctx context.Context, name, path string) error
	DeleteProject(ctx context.Context, name string) error
	ListPrompts(ctx context.Context) ([]store.Prompt, error)
	CreatePrompt(ctx context.Context, projectName, taskName, promptText string) (store.Prompt, error)
	UpdatePromptState(ctx context.Context, id int64, state string) error
}

type Server struct {
	store      Store
	dispatcher *dispatch.Dispatcher
	router     chi.Router
}

func New(st Store, d *dispatch.Dispatcher) *Server {
	s := &Server{store: st, dispatcher: d}
	s.router = s.buildRouter()
	return s
}

func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	r.Get("/health", s.health)
	r.Get("/projects", s.listProjects)
	r.Post("/projects", s.createProject)
	r.Put("/projects/{name}", s.updateProject)
	r.Delete("/projects/{name}", s.deleteProject)
	r.Get("/prompts", s.listPrompts)
	r.Post("/prompts", s.createPrompt)
	r.Put("/prompts/{id}", s.updatePromptState)
	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListProjects(r.Context())
	if err != nil {
		slog.Error("projects: list projects failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var body store.Project
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Path == "" {
		writeError(w, http.StatusBadRequest, "name and path are required")
		return
	}
	if err := s.store.CreateProject(r.Context(), body); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "project already exists")
			return
		}
		slog.Error("projects: create project failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, body)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if err := s.store.UpdateProject(r.Context(), name, body.Path); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		slog.Error("projects: update project failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if err := s.store.DeleteProject(r.Context(), name); err != nil {
		if errors.Is(err, store.ErrHasPrompts) {
			writeError(w, http.StatusConflict, "project has prompts; delete them first")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		slog.Error("projects: delete project failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPrompts(w http.ResponseWriter, r *http.Request) {
	prompts, err := s.store.ListPrompts(r.Context())
	if err != nil {
		slog.Error("projects: list prompts failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, prompts)
}

func (s *Server) createPrompt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectName string `json:"project_name"`
		TaskName    string `json:"task_name"`
		PromptText  string `json:"prompt_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		body.ProjectName == "" || body.TaskName == "" || body.PromptText == "" {
		writeError(w, http.StatusBadRequest, "project_name, task_name and prompt_text are required")
		return
	}
	prompt, err := s.store.CreatePrompt(r.Context(), body.ProjectName, body.TaskName, body.PromptText)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "project not found")
			return
		}
		slog.Error("projects: create prompt failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, prompt)
	if s.dispatcher != nil {
		// Dispatch off context.Background(), not r.Context(): if the
		// client disconnects or a proxy timeout fires between a
		// successful launch and MarkCreatingSpec inside
		// TryDispatchNext, a canceled r.Context() could leave the row
		// NOT_STARTED while a real claude process is already running,
		// risking a duplicate launch on the next dispatch trigger.
		go dispatchNextSafely(s.dispatcher)
	}
}

func (s *Server) updatePromptState(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !validState(body.State) {
		writeError(w, http.StatusBadRequest, "invalid state")
		return
	}
	if err := s.store.UpdatePromptState(r.Context(), id, body.State); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "prompt not found")
			return
		}
		slog.Error("projects: update prompt state failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
	if s.dispatcher != nil {
		// A manual state edit back to NOT_STARTED is the documented
		// way to unstick a permanently-stuck CREATING_SPEC prompt —
		// trigger a dispatch attempt so that recovery path actually
		// frees the slot immediately, not just whenever some other
		// prompt happens to get created or notify next.
		// TryDispatchNext is a no-op unless something is NOT_STARTED
		// and nothing is CREATING_SPEC, so calling it after every
		// successful state PUT (including ones that don't logically
		// need it) is harmless.
		go dispatchNextSafely(s.dispatcher)
	}
}

func validState(s string) bool {
	switch s {
	case store.StateNotStarted, store.StateCreatingSpec, store.StateImplementing, store.StateDone:
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
