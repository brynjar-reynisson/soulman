package subtitles_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"soulman/web-svc/subtitles"
)

// makeMKV builds a 2-second test video with an English (default) and a
// Swedish SubRip track, or skips the test if ffmpeg isn't installed.
func makeMKV(t *testing.T) string {
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
	out := filepath.Join(dir, "movie.mkv")
	cmd := exec.Command(ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "color=c=black:s=64x64:d=2",
		"-i", eng, "-i", swe,
		"-map", "0", "-map", "1", "-map", "2", "-c:v", "libx264", "-c:s", "srt",
		"-metadata:s:s:0", "language=eng", "-metadata:s:s:0", "title=English", "-disposition:s:0", "default",
		"-metadata:s:s:1", "language=swe", "-metadata:s:s:1", "title=Swedish", "-disposition:s:1", "0",
		out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not build test mkv: %v: %s", err, b)
	}
	return out
}

func TestTracks_ListsTextTracksWithDefault(t *testing.T) {
	svc := subtitles.New("", t.TempDir())
	tracks, err := svc.Tracks(context.Background(), makeMKV(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 {
		t.Fatalf("tracks = %+v, want 2", tracks)
	}
	if tracks[0].Language != "eng" || !tracks[0].Default || tracks[0].Title != "English" {
		t.Errorf("track 0 = %+v", tracks[0])
	}
	if tracks[1].Language != "swe" || tracks[1].Default || tracks[1].Index != 1 {
		t.Errorf("track 1 = %+v", tracks[1])
	}
}

func TestVTT_ConvertsAndCaches_ConcurrentCallsShareOneRun(t *testing.T) {
	svc := subtitles.New("", t.TempDir())
	file := makeMKV(t)

	var wg sync.WaitGroup
	paths := make([]string, 4)
	for i := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := svc.VTT(context.Background(), file, 1)
			if err != nil {
				t.Errorf("VTT: %v", err)
			}
			paths[i] = p
		}()
	}
	wg.Wait()
	for _, p := range paths {
		if p != paths[0] || p == "" {
			t.Fatalf("paths differ: %v", paths)
		}
	}
	body, _ := os.ReadFile(paths[0])
	if !strings.HasPrefix(string(body), "WEBVTT") || !strings.Contains(string(body), "Hej vännen") {
		t.Errorf("unexpected VTT:\n%s", body)
	}
}

func TestVTT_UnknownTrack(t *testing.T) {
	svc := subtitles.New("", t.TempDir())
	if _, err := svc.VTT(context.Background(), makeMKV(t), 7); !errors.Is(err, subtitles.ErrNoSuchTrack) {
		t.Errorf("err = %v, want ErrNoSuchTrack", err)
	}
}

func TestUnavailable_DegradesToNoTracks(t *testing.T) {
	svc := subtitles.New(filepath.Join(t.TempDir(), "no-such-ffmpeg.exe"), t.TempDir())
	if svc.Available() {
		t.Fatal("Available() = true for a missing ffmpeg")
	}
	tracks, err := svc.Tracks(context.Background(), "whatever.mkv")
	if err != nil || len(tracks) != 0 {
		t.Errorf("Tracks = %v, %v; want empty, nil", tracks, err)
	}
	if _, err := svc.VTT(context.Background(), "whatever.mkv", 0); !errors.Is(err, subtitles.ErrNoSuchTrack) {
		t.Errorf("VTT err = %v, want ErrNoSuchTrack", err)
	}
}
