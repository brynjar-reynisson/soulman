# Video subtitles — design (2026-11-04)

**Problem.** `4K-NC_The Princess Bride - 1987.mkv` has five embedded SubRip subtitle tracks (Danish, Finnish, Norwegian, Swedish, English — English flagged default). The player showed no subtitle button and never auto-selected English: browsers do not expose subtitles embedded in Matroska to `<video>`.

**Decision (agreed in conversation).** Convert embedded text tracks to WebVTT server-side and feed them to the player as `<track>` elements; English (the file's default) is on by default and all languages are selectable in the browser's CC menu.

## Design
- `web-svc/subtitles`: `ffprobe` track listing; one `ffmpeg` pass converting all text tracks to WebVTT; disk cache keyed by path+size+mtime; single-flight conversion.
- Routes `GET /vid/{token}/subtitles` and `/vid/{token}/subtitles/{n}.vtt`, authorised by the same signed video token as the stream (so shared links get subtitles too).
- Player waits for the track list, renders `<track>`s, default = file's default flag, else English, else none; `C` toggles captions.
- `web.ffmpeg_path` optional; missing ffmpeg degrades to "no subtitles".

## Claude's calls
- Convert all tracks in one pass and cache, rather than on-demand per track (same I/O cost; avoids repeating a multi-GB read per language).
- Kick off conversion when the list is requested, so the default track is usually ready by the time the browser asks for it.
- Leave `ffmpeg_path` blank in `config/prod.json` (PATH lookup) rather than hard-code a version-specific WinGet path that breaks on upgrade.

## Non-goals
Audio track switching; OCR of image subtitles (PGS/DVD); burning subtitles into video; uploading external `.srt` files.
