package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"soulman/web-svc/obsidian"
)

func (s *Server) obsidianFolders(w http.ResponseWriter, r *http.Request) {
	folders, err := obsidian.ListFolders(s.cfg.ObsidianRoot)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"folders": folders})
}

func (s *Server) obsidianFiles(w http.ResponseWriter, r *http.Request) {
	folder := r.URL.Query().Get("folder")
	files, err := obsidian.ListFiles(s.cfg.ObsidianRoot, folder)
	if err != nil {
		writeObsidianError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"files": files})
}

func (s *Server) obsidianFileGet(w http.ResponseWriter, r *http.Request) {
	folder := r.URL.Query().Get("folder")
	file := r.URL.Query().Get("file")
	content, err := obsidian.ReadFile(s.cfg.ObsidianRoot, folder, file)
	if err != nil {
		writeObsidianError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": content})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeObsidianError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, obsidian.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not found")
	case errors.Is(err, obsidian.ErrExists):
		writeJSONError(w, http.StatusConflict, "already exists")
	case errors.Is(err, obsidian.ErrInvalidName):
		writeJSONError(w, http.StatusBadRequest, "invalid name")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}
