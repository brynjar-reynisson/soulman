package httpserver_test

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"soulman/web-svc/auth"
	"soulman/web-svc/httpserver"
)

// newSubtitleServer serves a generated .mkv holding an English (default) and
// a Swedish subtitle track, or skips if ffmpeg isn't installed.
func newSubtitleServer(t *testing.T) (*httpserver.Server, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	srt := func(name, text string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("1\n00:00:00,500 --> 00:00:01,500\n"+text+"\n"), 0o644)
		return p
	}
	eng, swe := srt("eng.srt", "Hello"), srt("swe.srt", "Hej vännen")
	videos := t.TempDir()
	cmd := exec.Command(ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "color=c=black:s=64x64:d=2", "-i", eng, "-i", swe,
		"-map", "0", "-map", "1", "-map", "2", "-c:v", "libx264", "-c:s", "srt",
		"-metadata:s:s:0", "language=eng", "-metadata:s:s:0", "title=English", "-disposition:s:0", "default",
		"-metadata:s:s:1", "language=swe", "-metadata:s:s:1", "title=Swedish",
		filepath.Join(videos, "movie.mkv"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not build test mkv: %v: %s", err, b)
	}
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		VideoRoot:         videos,
		VideoShareSecret:  videoSecret,
		VideoShareTTL:     time.Hour,
		SubtitleCacheDir:  t.TempDir(),
	}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))
	rec := do(t, srv, "POST", "/api/videos/token?path=&file=movie.mkv", true, nil)
	var body struct{ Token string }
	json.NewDecoder(rec.Body).Decode(&body)
	if body.Token == "" {
		t.Fatalf("token request failed: %d %s", rec.Code, rec.Body.String())
	}
	return srv, body.Token
}

func TestVideoSubtitles_ListsTracksAndServesVTT_PublicWithToken(t *testing.T) {
	srv, tok := newSubtitleServer(t)

	rec := do(t, srv, "GET", "/vid/"+tok+"/subtitles", false, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Tracks []struct {
			Index    int
			Language string
			Title    string
			Default  bool
		}
	}
	json.NewDecoder(rec.Body).Decode(&list)
	if len(list.Tracks) != 2 || list.Tracks[0].Language != "eng" || !list.Tracks[0].Default || list.Tracks[1].Language != "swe" {
		t.Fatalf("tracks = %+v", list.Tracks)
	}

	rec = do(t, srv, "GET", "/vid/"+tok+"/subtitles/1.vtt", false, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("vtt status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/vtt") {
		t.Errorf("Content-Type = %q, want text/vtt", ct)
	}
	if body := rec.Body.String(); !strings.HasPrefix(body, "WEBVTT") || !strings.Contains(body, "Hej vännen") {
		t.Errorf("unexpected VTT:\n%s", body)
	}
}

func TestVideoSubtitles_BadRequests(t *testing.T) {
	srv, tok := newSubtitleServer(t)
	for _, tc := range []struct{ name, url string }{
		{"unknown track", "/vid/" + tok + "/subtitles/9.vtt"},
		{"not a number", "/vid/" + tok + "/subtitles/abc.vtt"},
		{"missing extension", "/vid/" + tok + "/subtitles/0"},
		{"traversal", "/vid/" + tok + "/subtitles/..%2f..%2fx.vtt"},
		{"bad token", "/vid/bogus/subtitles/0.vtt"},
		{"bad token list", "/vid/bogus/subtitles"},
	} {
		if code := do(t, srv, "GET", tc.url, false, nil).Code; code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", tc.name, code)
		}
	}
}

func TestVideoSubtitles_NoFFmpeg_EmptyList(t *testing.T) {
	srv, _ := newVideoServer(t) // no SubtitleCacheDir/FFmpegPath; mp4 test fixture
	rec := do(t, srv, "POST", "/api/videos/token?path=trips&file=beach.mp4", true, nil)
	var body struct{ Token string }
	json.NewDecoder(rec.Body).Decode(&body)
	rec = do(t, srv, "GET", "/vid/"+body.Token+"/subtitles", false, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"tracks":[]`) {
		t.Errorf("status = %d body = %s, want 200 with empty tracks", rec.Code, rec.Body.String())
	}
}
