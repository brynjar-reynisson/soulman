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

type fileEntryResponse struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func (s *Server) filesList(w http.ResponseWriter, r *http.Request) {
	root, ok := findFileBrowserRoot(s.cfg.FileBrowserRoots, r.URL.Query().Get("root"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown root")
		return
	}
	folders, files, err := filebrowser.List(root, r.URL.Query().Get("path"))
	if err != nil {
		writeFileBrowserError(w, err)
		return
	}
	resp := make([]fileEntryResponse, len(files))
	for i, f := range files {
		resp[i] = fileEntryResponse{Name: f.Name, Size: f.Size}
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": folders, "files": resp})
}

func (s *Server) filesDownload(w http.ResponseWriter, r *http.Request) {
	root, ok := findFileBrowserRoot(s.cfg.FileBrowserRoots, r.URL.Query().Get("root"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown root")
		return
	}
	filename := r.URL.Query().Get("file")
	absPath, err := filebrowser.ResolveFile(root, r.URL.Query().Get("path"), filename)
	if err != nil {
		writeFileBrowserError(w, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	http.ServeFile(w, r, absPath)
}

// maxUploadBytes caps a single upload's request body. Hardcoded rather
// than added to the config schema — bump here if 200MB turns out wrong
// (YAGNI, same posture as obsidian's no-size-cap call, just starting
// from *some* cap instead of none given this is binary/large-file
// upload rather than markdown notes).
const maxUploadBytes = 200 << 20 // 200MB

func (s *Server) filesUpload(w http.ResponseWriter, r *http.Request) {
	root, ok := findFileBrowserRoot(s.cfg.FileBrowserRoots, r.URL.Query().Get("root"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown root")
		return
	}
	overwrite := r.URL.Query().Get("overwrite") == "true"

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "upload too large")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "invalid multipart body")
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	if err := filebrowser.Save(root, r.URL.Query().Get("path"), header.Filename, file, overwrite); err != nil {
		writeFileBrowserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
