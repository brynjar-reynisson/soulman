import { useEffect, useRef, useState } from 'react';
import { getAccessToken } from '../auth';
import {
  ApiError,
  getPublicVideoInfo,
  getVideoToken,
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

type FullscreenVideo = HTMLVideoElement & { webkitEnterFullscreen?: () => void };

export function VideoPlayerPage({ source }: { source: VideoPlayerSource }) {
  const [token, setToken] = useState<string | null>(source.kind === 'public' ? source.token : null);
  const [name, setName] = useState<string>(source.kind === 'owner' ? source.file : '');
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const videoRef = useRef<FullscreenVideo | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);

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
      {!error && !token && <p className="text-sm text-gray-400">Loading...</p>}
      {!error && token && (
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
            />
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
              Space play/pause · F full screen · M mute · ←/→ seek · ↑/↓ volume
            </span>
          </div>
        </>
      )}
    </div>
  );
}
