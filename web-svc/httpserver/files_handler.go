package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"soulman/web-svc/filebrowser"
)

type fileBrowserRootResponse struct {
	Label  string `json:"label"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

func (s *Server) filesRoots(w http.ResponseWriter, r *http.Request) {
	listings := filebrowser.ListRoots(s.cfg.FileBrowserRoots)
	resp := make([]fileBrowserRootResponse, len(listings))
	for i, l := range listings {
		resp[i] = fileBrowserRootResponse{Label: l.Label, Path: l.Path, Exists: l.Exists}
	}
	writeJSON(w, http.StatusOK, map[string][]fileBrowserRootResponse{"roots": resp})
}

func findFileBrowserRoot(roots []filebrowser.Root, label string) (filebrowser.Root, bool) {
	for _, r := range roots {
		if r.Label == label {
			return r, true
		}
	}
	return filebrowser.Root{}, false
}

func writeFileBrowserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, filebrowser.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not found")
	case errors.Is(err, filebrowser.ErrExists):
		writeJSONError(w, http.StatusConflict, "already exists")
	case errors.Is(err, filebrowser.ErrInvalidName):
		writeJSONError(w, http.StatusBadRequest, "invalid name")
	default:
		slog.Error("file browser unexpected error", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}
