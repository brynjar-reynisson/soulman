package httpserver

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"soulman/web-svc/filebrowser"
	"soulman/web-svc/sharelink"
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
	serveFileDownload(w, r, absPath, filename)
}

// serveFileDownload streams absPath as an attachment named filename,
// applying the same Content-Disposition/Cache-Control/UTF-8-BOM treatment
// regardless of which route reached it — the owner-JWT-gated
// /api/files/download, or the token-gated /dl/{token} share link.
func serveFileDownload(w http.ResponseWriter, r *http.Request, absPath, filename string) {
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// This must never be served from a browser or edge (Cloudflare tunnel)
	// cache: a file's content can change between browses, and — the
	// concrete incident that motivated this — http.ServeFile's
	// Last-Modified header alone was enough for a browser to silently
	// replay a stale cached response after this handler's encoding
	// behavior changed server-side, making the fix look like it hadn't
	// deployed at all even though the origin was already correct.
	w.Header().Set("Cache-Control", "no-store")

	needsBOM, err := isBOMlessUTF8Text(absPath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if needsBOM {
		serveWithUTF8BOM(w, absPath)
		return
	}
	http.ServeFile(w, r, absPath)
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// isBOMlessUTF8Text sniffs a file the same way http.ServeFile would (first
// 512 bytes) and reports whether it's UTF-8 text containing at least one
// non-ASCII byte, with no BOM already present. A real download of an
// Icelandic-language .txt file confirmed this matters: several Android
// text-viewer apps misdetect BOM-less UTF-8 as a legacy codepage and
// render accented characters as mojibake, even though the bytes
// themselves — and the served Content-Type — are already correctly
// UTF-8; a leading BOM is what those viewers actually key off of.
// Pure-ASCII text is left untouched (valid under either interpretation,
// so there's nothing to disambiguate and no reason to change what gets
// served), as are binary files and text that already carries a BOM.
func isBOMlessUTF8Text(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}
	buf = buf[:n]
	if bytes.HasPrefix(buf, utf8BOM) {
		return false, nil
	}
	if !hasNonASCIIByte(buf) {
		return false, nil
	}
	return strings.HasPrefix(http.DetectContentType(buf), "text/plain"), nil
}

func hasNonASCIIByte(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return true
		}
	}
	return false
}

// serveWithUTF8BOM streams path with a UTF-8 BOM prepended. This bypasses
// http.ServeFile's Range/conditional-GET support — an accepted tradeoff
// for the small text files this path actually triggers on; binary files
// and already-BOM'd text (the cases that matter for large/resumable
// downloads) still go through http.ServeFile in filesDownload above.
func serveWithUTF8BOM(w http.ResponseWriter, path string) {
	f, err := os.Open(path)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size()+int64(len(utf8BOM)), 10))
	w.Write(utf8BOM)
	io.Copy(w, f)
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

func (s *Server) filesShare(w http.ResponseWriter, r *http.Request) {
	root, ok := findFileBrowserRoot(s.cfg.FileBrowserRoots, r.URL.Query().Get("root"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown root")
		return
	}
	relPath := r.URL.Query().Get("path")
	filename := r.URL.Query().Get("file")
	if _, err := filebrowser.ResolveFile(root, relPath, filename); err != nil {
		writeFileBrowserError(w, err)
		return
	}
	token, expiresAt := sharelink.Issue(s.cfg.ShareLinkSecret, root.Label, relPath, filename, s.cfg.ShareLinkTTL)
	writeJSON(w, http.StatusOK, map[string]any{
		"url":       "/dl/" + token,
		"expiresAt": expiresAt,
	})
}
