package httpserver

import (
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"soulman/web-svc/filebrowser"
	"soulman/web-svc/sharelink"
)

// videoRootLabel is the sharelink "root" claim for video tokens. Verify
// rejects any token whose root differs, so a file-share token can never
// open the video routes (and the two use different signing secrets anyway).
const videoRootLabel = "videos"

// videoPathPrefix is the URL prefix of the unauthenticated, token-gated
// video routes — see requestLogger, which redacts it from the request log.
const videoPathPrefix = "/vid/"

var videoMIMETypes = map[string]string{
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".webm": "video/webm",
	".ogv":  "video/ogg",
	".mov":  "video/quicktime",
	".mkv":  "video/x-matroska",
}

func isVideoFile(name string) bool {
	_, ok := videoMIMETypes[strings.ToLower(filepath.Ext(name))]
	return ok
}

func (s *Server) videoRoot() filebrowser.Root {
	return filebrowser.Root{Label: videoRootLabel, Path: s.cfg.VideoRoot}
}

// videosList lists subfolders and video files (only) of one folder under the
// video root. Owner-JWT-gated.
func (s *Server) videosList(w http.ResponseWriter, r *http.Request) {
	folders, files, err := filebrowser.List(s.videoRoot(), r.URL.Query().Get("path"))
	if err != nil {
		writeFileBrowserError(w, err)
		return
	}
	videos := []fileEntryResponse{}
	for _, f := range files {
		if isVideoFile(f.Name) {
			videos = append(videos, fileEntryResponse{Name: f.Name, Size: f.Size})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": folders, "files": videos})
}

// videosToken issues the signed token for one video. The owner's player uses
// it as its stream URL (a <video> element can't send an Authorization
// header), and the same token is the shareable link. Owner-JWT-gated.
func (s *Server) videosToken(w http.ResponseWriter, r *http.Request) {
	relPath := r.URL.Query().Get("path")
	filename := r.URL.Query().Get("file")
	if !isVideoFile(filename) {
		writeJSONError(w, http.StatusBadRequest, "not a video file")
		return
	}
	if _, err := filebrowser.ResolveFile(s.videoRoot(), relPath, filename); err != nil {
		writeFileBrowserError(w, err)
		return
	}
	token, expiresAt := sharelink.Issue(s.cfg.VideoShareSecret, videoRootLabel, relPath, filename, s.cfg.VideoShareTTL)
	if token == "" {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"name":      filename,
		"expiresAt": expiresAt,
	})
}

// resolveVideoToken verifies a token and resolves it to a file on disk. On
// failure it writes the response itself and returns ok=false. Every
// invalid cause shares one message so the failure can't be used to probe.
func (s *Server) resolveVideoToken(w http.ResponseWriter, r *http.Request) (absPath, name string, ok bool) {
	ip := clientIP(r)
	if s.videoFailures.blocked(ip) {
		writeJSONError(w, http.StatusTooManyRequests, "too many invalid requests")
		return "", "", false
	}
	root, relPath, filename, err := sharelink.Verify(s.cfg.VideoShareSecret, chi.URLParam(r, "token"))
	if errors.Is(err, sharelink.ErrExpired) {
		writeJSONError(w, http.StatusGone, "This link has expired.")
		return "", "", false
	}
	if err == nil && root != videoRootLabel {
		err = sharelink.ErrInvalid
	}
	if err == nil && !isVideoFile(filename) {
		err = sharelink.ErrInvalid
	}
	if err == nil {
		absPath, err = filebrowser.ResolveFile(s.videoRoot(), relPath, filename)
	}
	if err != nil {
		s.videoFailures.record(ip)
		writeJSONError(w, http.StatusNotFound, "This link is invalid or the video is no longer available.")
		return "", "", false
	}
	return absPath, filename, true
}

// videoInfo returns the video's display name for the public player page.
func (s *Server) videoInfo(w http.ResponseWriter, r *http.Request) {
	_, name, ok := s.resolveVideoToken(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// videoStream serves the video inline with Range support (http.ServeFile),
// which browsers need for seeking and for Safari/iOS playback at all.
func (s *Server) videoStream(w http.ResponseWriter, r *http.Request) {
	absPath, name, ok := s.resolveVideoToken(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", videoMIMETypes[strings.ToLower(filepath.Ext(name))])
	w.Header().Set("Content-Disposition", `inline; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, absPath)
}

// clientIP prefers Cloudflare's header (web-svc sits behind a tunnel, so
// RemoteAddr is always localhost). A spoofed header only lets a caller dodge
// their own rate limit; it can't reach anything the token doesn't allow.
func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// failureLimiter counts invalid-token attempts per IP in a sliding window.
// Valid requests (the many Range requests of normal playback) never count.
type failureLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	max      int
	window   time.Duration
}

func newFailureLimiter(max int, window time.Duration) *failureLimiter {
	return &failureLimiter{failures: map[string][]time.Time{}, max: max, window: window}
}

func (l *failureLimiter) prune(ip string, now time.Time) []time.Time {
	recent := l.failures[ip][:0]
	for _, t := range l.failures[ip] {
		if now.Sub(t) < l.window {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(l.failures, ip)
	} else {
		l.failures[ip] = recent
	}
	return recent
}

func (l *failureLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(ip, time.Now())) >= l.max
}

func (l *failureLimiter) record(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	recent := l.prune(ip, now)
	l.failures[ip] = append(recent, now)
}
