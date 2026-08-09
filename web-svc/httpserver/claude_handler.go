package httpserver

import (
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
