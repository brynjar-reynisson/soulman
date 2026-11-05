// Package subtitles exposes the text subtitle tracks embedded in a video
// container (typically .mkv) as WebVTT, which is what a browser <track>
// element understands — browsers cannot read subtitles embedded in a
// container themselves. It shells out to ffprobe (list tracks) and ffmpeg
// (convert), and caches the converted files on disk. Everything degrades to
// "no subtitles" when ffmpeg is not installed.
package subtitles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNoSuchTrack is returned for a track ordinal the file does not have (or
// one that is image-based and cannot be converted to text).
var ErrNoSuchTrack = errors.New("subtitles: no such track")

// textCodecs are the subtitle codecs ffmpeg can convert to WebVTT. Image
// based ones (hdmv_pgs_subtitle, dvd_subtitle, dvb_subtitle) are excluded.
var textCodecs = map[string]bool{
	"subrip": true, "srt": true, "ass": true, "ssa": true,
	"webvtt": true, "mov_text": true, "text": true,
}

// Track describes one convertible subtitle track. Index is the track's
// position among all subtitle streams in the file (ffmpeg's 0:s:N), not the
// container's stream index.
type Track struct {
	Index    int    `json:"index"`
	Language string `json:"language"`
	Title    string `json:"title"`
	Default  bool   `json:"default"`
}

type call struct {
	done chan struct{}
	err  error
}

// Service lists and converts subtitle tracks. The zero value is unusable;
// construct with New.
type Service struct {
	ffmpeg   string
	ffprobe  string
	cacheDir string

	mu       sync.Mutex
	inflight map[string]*call
}

// New locates ffmpeg and ffprobe. ffmpegPath may be empty, in which case
// both are looked up on PATH; if only ffmpegPath is given, ffprobe is
// expected beside it. The returned Service reports Available() == false if
// either tool can't be found.
func New(ffmpegPath, cacheDir string) *Service {
	s := &Service{cacheDir: cacheDir, inflight: map[string]*call{}}
	if ffmpegPath != "" {
		s.ffmpeg = ffmpegPath
		ext := filepath.Ext(ffmpegPath)
		s.ffprobe = filepath.Join(filepath.Dir(ffmpegPath), "ffprobe"+ext)
	} else {
		s.ffmpeg, _ = exec.LookPath("ffmpeg")
		s.ffprobe, _ = exec.LookPath("ffprobe")
	}
	if _, err := os.Stat(s.ffmpeg); err != nil {
		s.ffmpeg = ""
	}
	if _, err := os.Stat(s.ffprobe); err != nil {
		s.ffprobe = ""
	}
	return s
}

func (s *Service) Available() bool { return s != nil && s.ffmpeg != "" && s.ffprobe != "" }

// Tracks lists the convertible subtitle tracks of file.
func (s *Service) Tracks(ctx context.Context, file string) ([]Track, error) {
	if !s.Available() {
		return []Track{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, s.ffprobe, "-v", "error", "-select_streams", "s",
		"-show_entries", "stream=codec_name:stream_tags=language,title:stream_disposition=default",
		"-of", "json", file).Output()
	if err != nil {
		return nil, fmt.Errorf("subtitles: ffprobe: %w", err)
	}
	var probed struct {
		Streams []struct {
			CodecName   string            `json:"codec_name"`
			Tags        map[string]string `json:"tags"`
			Disposition struct {
				Default int `json:"default"`
			} `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &probed); err != nil {
		return nil, fmt.Errorf("subtitles: parsing ffprobe output: %w", err)
	}
	tracks := []Track{}
	for i, st := range probed.Streams {
		if !textCodecs[st.CodecName] {
			continue
		}
		tracks = append(tracks, Track{
			Index:    i,
			Language: st.Tags["language"],
			Title:    st.Tags["title"],
			Default:  st.Disposition.Default == 1,
		})
	}
	return tracks, nil
}

// cacheKey changes whenever the file's path, size or modification time does,
// so replacing a video never serves stale subtitles.
func cacheKey(file string) (string, error) {
	info, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", file, info.Size(), info.ModTime().UnixNano())))
	return hex.EncodeToString(sum[:])[:20], nil
}

// Warm starts converting every text track of file in the background (a
// single ffmpeg pass reads the whole container, so converting all tracks at
// once costs the same as converting one). Errors are returned only by VTT.
func (s *Service) Warm(file string) {
	if !s.Available() {
		return
	}
	go func() { _, _ = s.VTT(context.Background(), file, -1) }()
}

// VTT returns the path of the cached WebVTT file for track ordinal, running
// the conversion first if needed. Concurrent callers for the same file share
// one ffmpeg run. ordinal -1 just ensures the conversion has happened.
func (s *Service) VTT(ctx context.Context, file string, ordinal int) (string, error) {
	if !s.Available() {
		return "", ErrNoSuchTrack
	}
	key, err := cacheKey(file)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(s.cacheDir, key)
	if _, err := os.Stat(filepath.Join(dir, "done")); err != nil {
		if err := s.convert(ctx, file, key, dir); err != nil {
			return "", err
		}
	}
	if ordinal < 0 {
		return "", nil
	}
	path := filepath.Join(dir, strconv.Itoa(ordinal)+".vtt")
	if _, err := os.Stat(path); err != nil {
		return "", ErrNoSuchTrack
	}
	return path, nil
}

func (s *Service) convert(ctx context.Context, file, key, dir string) error {
	s.mu.Lock()
	if c, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		select {
		case <-c.done:
			return c.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c := &call{done: make(chan struct{})}
	s.inflight[key] = c
	s.mu.Unlock()

	// Detached from the first caller's ctx on purpose: that request may be
	// abandoned (page closed) while other waiters still need the result.
	c.err = s.runFFmpeg(file, dir)
	s.mu.Lock()
	delete(s.inflight, key)
	s.mu.Unlock()
	close(c.done)
	return c.err
}

func (s *Service) runFFmpeg(file, dir string) error {
	tracks, err := s.Tracks(context.Background(), file)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.cacheDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(s.cacheDir, "tmp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	args := []string{"-v", "error", "-y", "-i", file}
	for _, t := range tracks {
		args = append(args, "-map", "0:s:"+strconv.Itoa(t.Index), "-f", "webvtt",
			filepath.Join(tmp, strconv.Itoa(t.Index)+".vtt"))
	}
	if len(tracks) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, s.ffmpeg, args...)
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("subtitles: ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "done"), nil, 0o644); err != nil {
		return err
	}
	// Publish atomically: a half-written cache dir is never visible.
	if err := os.Rename(tmp, dir); err != nil {
		if _, statErr := os.Stat(filepath.Join(dir, "done")); statErr == nil {
			return nil // another process won the race with identical content
		}
		return err
	}
	return nil
}
