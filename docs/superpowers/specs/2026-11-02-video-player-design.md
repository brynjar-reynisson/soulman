# Video player page — design (2026-11-02)

**Goal.** A Videos page on the web dashboard that browses a configurable folder and plays videos, where the owner can hand a friend or family member a link that plays one video with no sign-in.

## Decisions (agreed in conversation)
- Folder: `web.video_root`, default `C:\Users\Lenovo\Documents\videos`; directory/file list as nested accordions, like the Diary page.
- Dashboard links are alphabetical (Claude, Diary, Files, Obsidian, Projects, Videos, Web-Search) with **Sign out** always last.
- Selecting a video opens a player page with a `← Soulman` link.
- **Sharing:** a "Copy share link" button on the owner's player page issues a signed, expiring link (`/?v=<token>`). Plain guessable links to arbitrary files were rejected because anyone could probe the folder by guessing filenames.
- Audience is friends/family watching within a few days: TTL **7 days** (`web.video_share_ttl_minutes`).
- A link holder sees only the player: no `← Soulman`, no folder access, no share button.
- Player: native `<video controls>` (play/pause, seek, volume, speed, fullscreen, PiP as the browser provides), an explicit Full screen button, double-click fullscreen, and keyboard shortcuts (Space/K, F, M, arrows).

## Claude's calls
- Per-IP rate limit on failed token attempts (20/min), second layer after unguessable tokens.
- Separate **persisted** signing secret (`$SOULMAN_ROOT\video-share-secret`) so links survive web-svc restarts; deleting it revokes all links.
- Owner playback also uses a token, since `<video>` cannot send an Authorization header.
- Only mp4, m4v, webm, ogv, mov, mkv are listed; browser codec support decides whether a given file actually plays (mkv/mov often won't in Chrome/Firefox).

## Non-goals
Transcoding, thumbnails, playlists, resume position, subtitles, per-link revocation, upload.
