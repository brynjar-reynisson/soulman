import { useEffect, useRef, useState } from 'react';
import { getAccessToken } from '../auth';
import {
  ApiError,
  getPublicVideoInfo,
  getVideoSubtitles,
  getVideoToken,
  subtitleTrackUrl,
  type SubtitleTrack,
  videoShareUrl,
  videoStreamUrl,
} from '../api';

// One page for two audiences. The owner (path + file + onBack) is signed in,
// gets a "← Soulman" link and a share button. A visitor holding a share link
// (token only) is anonymous: no back link, since there is nowhere for them to
// go back to. Either way the <video> streams from /vid/<token>/stream.
export type VideoPlayerSource =
  | { kind: 'owner'; path: string; file: string; onBack: () => void }
  | { kind: 'public'; token: string };

function describeError(err: unknown): string {
  if (err instanceof ApiError && err.status === 410) return 'This link has expired.';
  if (err instanceof ApiError && err.status === 429) return 'Too many attempts — try again in a minute.';
  return 'This link is invalid or the video is no longer available.';
}

// ISO 639-2 (what Matroska stores) to the BCP 47 codes <track srclang> wants.
const LANGUAGE_CODES: Record<string, string> = {
  eng: 'en', dan: 'da', fin: 'fi', nor: 'no', nob: 'nb', swe: 'sv', ice: 'is', isl: 'is',
  ger: 'de', deu: 'de', fre: 'fr', fra: 'fr', spa: 'es', ita: 'it', dut: 'nl', nld: 'nl',
  por: 'pt', pol: 'pl', rus: 'ru', jpn: 'ja', kor: 'ko', chi: 'zh', zho: 'zh',
};

// Containers like MP4 drop the track title, leaving only the language code;
// turn "swe" into "Swedish" for the subtitle menu.
function languageName(code: string): string | null {
  const bcp47 = LANGUAGE_CODES[code] ?? code;
  try {
    const name = new Intl.DisplayNames(['en'], { type: 'language' }).of(bcp47);
    return name && name !== bcp47 ? name : null;
  } catch {
    return null;
  }
}

function trackLabel(t: SubtitleTrack): string {
  return t.title || languageName(t.language) || t.language || `Track ${t.index + 1}`;
}

// The track to switch on automatically: the one the file flags as default,
// otherwise English if present, otherwise none.
function pickDefaultTrack(tracks: SubtitleTrack[]): number | null {
  const flagged = tracks.find((t) => t.default);
  if (flagged) return flagged.index;
  const english = tracks.find((t) => t.language === 'eng' || t.language === 'en');
  return english ? english.index : null;
}

type FullscreenVideo = HTMLVideoElement & { webkitEnterFullscreen?: () => void };

export function VideoPlayerPage({ source }: { source: VideoPlayerSource }) {
  const [token, setToken] = useState<string | null>(source.kind === 'public' ? source.token : null);
  const [name, setName] = useState<string>(source.kind === 'owner' ? source.file : '');
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  // null until the subtitle list has loaded (or failed); the <video> waits for
  // it so a default track is present when the element is created.
  const [tracks, setTracks] = useState<SubtitleTrack[] | null>(null);
  const videoRef = useRef<FullscreenVideo | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const defaultTrackLabel = useRef<string | null>(null);

  const ownerPath = source.kind === 'owner' ? source.path : null;
  const ownerFile = source.kind === 'owner' ? source.file : null;
  const publicToken = source.kind === 'public' ? source.token : null;

  useEffect(() => {
    let active = true;
    (async () => {
      try {
        if (publicToken !== null) {
          const info = await getPublicVideoInfo(publicToken);
          if (active) setName(info.name);
        } else if (ownerPath !== null && ownerFile !== null) {
          const issued = await getVideoToken(await getAccessToken(), ownerPath, ownerFile);
          if (active) {
            setToken(issued.token);
            setName(issued.name);
          }
        }
      } catch (err) {
        if (active) setError(describeError(err));
      }
    })();
    return () => {
      active = false;
    };
  }, [publicToken, ownerPath, ownerFile]);

  useEffect(() => {
    if (!token) return;
    let active = true;
    getVideoSubtitles(token)
      .then((t) => {
        if (!active) return;
        const def = t.find((x) => x.index === pickDefaultTrack(t));
        defaultTrackLabel.current = def ? trackLabel(def) : null;
        setTracks(t);
      })
      .catch(() => active && setTracks([]));
    return () => {
      active = false;
    };
  }, [token]);

  useEffect(() => {
    if (name) document.title = name;
  }, [name]);

  // Keyboard shortcuts people expect from a video page.
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      const video = videoRef.current;
      const target = e.target as HTMLElement | null;
      if (!video || e.ctrlKey || e.metaKey || e.altKey) return;
      if (target && /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName)) return;
      switch (e.key) {
        case ' ':
        case 'k':
          // Space on a focused button already activates that button.
          if (e.key === ' ' && target && target.tagName === 'BUTTON') return;
          e.preventDefault();
          if (video.paused) void video.play();
          else video.pause();
          break;
        case 'f':
          toggleFullscreen();
          break;
        case 'c': {
          // Toggle captions: off if any track is showing, else the default one.
          const list = Array.from(video.textTracks);
          if (list.length === 0) break;
          if (list.some((t) => t.mode === 'showing')) {
            list.forEach((t) => (t.mode = 'disabled'));
          } else {
            (list.find((t) => t.label === defaultTrackLabel.current) ?? list[0]).mode = 'showing';
          }
          break;
        }
        case 'm':
          video.muted = !video.muted;
          break;
        case 'ArrowLeft':
          video.currentTime = Math.max(0, video.currentTime - 5);
          break;
        case 'ArrowRight':
          video.currentTime = Math.min(video.duration || Infinity, video.currentTime + 5);
          break;
        case 'ArrowUp':
          e.preventDefault();
          video.volume = Math.min(1, video.volume + 0.1);
          break;
        case 'ArrowDown':
          e.preventDefault();
          video.volume = Math.max(0, video.volume - 0.1);
          break;
      }
    }
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, []);

  function toggleFullscreen() {
    const video = videoRef.current;
    const container = containerRef.current;
    if (!video || !container) return;
    if (document.fullscreenElement) {
      void document.exitFullscreen();
    } else if (container.requestFullscreen) {
      void container.requestFullscreen();
    } else if (video.webkitEnterFullscreen) {
      video.webkitEnterFullscreen(); // iPhone Safari can only fullscreen the <video> itself
    }
  }

  async function handleShare() {
    if (!token) return;
    try {
      await navigator.clipboard.writeText(videoShareUrl(token));
      setCopied(true);
    } catch {
      setError('Could not copy the link');
    }
  }

  return (
    <div className="min-h-screen bg-black p-4 text-white md:p-6">
      <div className="mb-4 flex items-center justify-between gap-4">
        <h1 className="truncate text-xl font-semibold md:text-2xl">{name || 'Video'}</h1>
        {source.kind === 'owner' && (
          <button onClick={source.onBack} className="shrink-0 text-sm text-white underline">
            ← Soulman
          </button>
        )}
      </div>
      {error && <p className="text-sm text-red-400">{error}</p>}
      {!error && (!token || tracks === null) && <p className="text-sm text-gray-400">Loading...</p>}
      {!error && token && tracks !== null && (
        <>
          <div ref={containerRef} className="mx-auto w-full max-w-5xl overflow-hidden rounded">
            <video
              ref={videoRef}
              src={videoStreamUrl(token)}
              controls
              playsInline
              autoFocus
              preload="metadata"
              onDoubleClick={toggleFullscreen}
              onError={() => setError('This video could not be played in your browser.')}
              className="max-h-[80vh] w-full bg-black"
            >
              {tracks.map((t) => (
                <track
                  key={t.index}
                  kind="subtitles"
                  src={subtitleTrackUrl(token, t.index)}
                  srcLang={LANGUAGE_CODES[t.language] ?? t.language ?? 'und'}
                  label={trackLabel(t)}
                  default={t.index === pickDefaultTrack(tracks)}
                />
              ))}
            </video>
          </div>
          <div className="mx-auto mt-3 flex max-w-5xl items-center gap-4 text-sm">
            <button onClick={toggleFullscreen} className="text-white underline">
              Full screen
            </button>
            {source.kind === 'owner' && (
              <>
                <button onClick={handleShare} className="text-white underline">
                  Copy share link
                </button>
                {copied && <span className="text-xs text-green-400">Link copied</span>}
              </>
            )}
            <span className="ml-auto hidden text-xs text-gray-400 md:inline">
              Space play/pause · F full screen · C captions · M mute · ←/→ seek · ↑/↓ volume
            </span>
          </div>
        </>
      )}
    </div>
  );
}
