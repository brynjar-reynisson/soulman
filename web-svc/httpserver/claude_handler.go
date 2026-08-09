package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"soulman/web-svc/claudesession"
)

type claudeRootResponse struct {
	Label   string   `json:"label"`
	Path    string   `json:"path"`
	Exists  bool     `json:"exists"`
	Folders []string `json:"folders"`
}

func (s *Server) claudeRoots(w http.ResponseWriter, r *http.Request) {
	listings := claudesession.ListRoots(s.cfg.ClaudeProjectRoots)
	resp := make([]claudeRootResponse, len(listings))
	for i, l := range listings {
		resp[i] = claudeRootResponse{Label: l.Label, Path: l.Path, Exists: l.Exists, Folders: l.Folders}
	}
	writeJSON(w, http.StatusOK, map[string][]claudeRootResponse{"roots": resp})
}

type claudeLaunchRequest struct {
	Root        string `json:"root"`
	Folder      string `json:"folder"`
	SessionName string `json:"sessionName"`
}

func (s *Server) claudeLaunch(w http.ResponseWriter, r *http.Request) {
	var req claudeLaunchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	root, ok := findClaudeRoot(s.cfg.ClaudeProjectRoots, req.Root)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown root")
		return
	}
	if err := claudesession.Launch(root, req.Folder, req.SessionName); err != nil {
		writeClaudeSessionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func findClaudeRoot(roots []claudesession.Root, label string) (claudesession.Root, bool) {
	for _, r := range roots {
		if r.Label == label {
			return r, true
		}
	}
	return claudesession.Root{}, false
}

func writeClaudeSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, claudesession.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not found")
	case errors.Is(err, claudesession.ErrInvalidName):
		writeJSONError(w, http.StatusBadRequest, "invalid name")
	case errors.Is(err, claudesession.ErrLaunchFailed):
		writeJSONError(w, http.StatusInternalServerError, "launch failed")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}
