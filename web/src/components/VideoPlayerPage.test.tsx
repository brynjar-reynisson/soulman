import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { VideoPlayerPage } from './VideoPlayerPage';
import * as api from '../api';

vi.mock('../auth', () => ({ getAccessToken: async () => 'jwt' }));

beforeEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(api, 'getVideoSubtitles').mockResolvedValue([]);
});

describe('VideoPlayerPage', () => {
  it('public visitor sees the player and no back link or share button', async () => {
    vi.spyOn(api, 'getPublicVideoInfo').mockResolvedValue({ name: 'beach.mp4' });
    const { container } = render(<VideoPlayerPage source={{ kind: 'public', token: 'tok' }} />);
    await screen.findByText('beach.mp4');
    await waitFor(() => expect(container.querySelector('video')).not.toBeNull());
    expect(container.querySelector('video')?.getAttribute('src')).toBe('/vid/tok/stream');
    expect(container.querySelector('video')?.hasAttribute('controls')).toBe(true);
    expect(screen.queryByText(/Soulman/)).toBeNull();
    expect(screen.queryByText('Copy share link')).toBeNull();
    expect(screen.getByText('Full screen')).toBeTruthy();
  });

  it('public visitor sees a friendly message for an expired link', async () => {
    vi.spyOn(api, 'getPublicVideoInfo').mockRejectedValue(new api.ApiError(410, 'gone'));
    render(<VideoPlayerPage source={{ kind: 'public', token: 'tok' }} />);
    expect(await screen.findByText('This link has expired.')).toBeTruthy();
  });

  it('owner gets the back link and can copy the share link', async () => {
    vi.spyOn(api, 'getVideoToken').mockResolvedValue({ token: 'abc', name: 'beach.mp4', expiresAt: '' });
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const onBack = vi.fn();
    render(<VideoPlayerPage source={{ kind: 'owner', path: 'trips', file: 'beach.mp4', onBack }} />);

    await userEvent.click(await screen.findByText('← Soulman'));
    expect(onBack).toHaveBeenCalled();

    await userEvent.click(await screen.findByText('Copy share link'));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(`${window.location.origin}/?v=abc`));
    expect(api.getVideoToken).toHaveBeenCalledWith('jwt', 'trips', 'beach.mp4');
  });

  it('adds a <track> per subtitle language and switches the default (English) on', async () => {
    vi.spyOn(api, 'getPublicVideoInfo').mockResolvedValue({ name: 'movie.mkv' });
    vi.spyOn(api, 'getVideoSubtitles').mockResolvedValue([
      { index: 0, language: 'dan', title: 'Danish', default: false },
      { index: 1, language: 'eng', title: 'English', default: true },
      { index: 2, language: 'swe', title: 'Swedish', default: false },
    ]);
    const { container } = render(<VideoPlayerPage source={{ kind: 'public', token: 'tok' }} />);
    await waitFor(() => expect(container.querySelectorAll('track').length).toBe(3));

    const tracks = Array.from(container.querySelectorAll('track'));
    expect(tracks.map((t) => t.getAttribute('label'))).toEqual(['Danish', 'English', 'Swedish']);
    expect(tracks.map((t) => t.getAttribute('srclang'))).toEqual(['da', 'en', 'sv']);
    expect(tracks[1].getAttribute('src')).toBe('/vid/tok/subtitles/1.vtt');
    expect(tracks.map((t) => t.hasAttribute('default'))).toEqual([false, true, false]);
  });

  it('falls back to English as default when the file flags none, and to none otherwise', async () => {
    vi.spyOn(api, 'getPublicVideoInfo').mockResolvedValue({ name: 'movie.mkv' });
    const spy = vi.spyOn(api, 'getVideoSubtitles').mockResolvedValue([
      { index: 0, language: 'fin', title: '', default: false },
      { index: 1, language: 'eng', title: '', default: false },
    ]);
    const first = render(<VideoPlayerPage source={{ kind: 'public', token: 'tok' }} />);
    await waitFor(() => expect(first.container.querySelectorAll('track').length).toBe(2));
    expect(Array.from(first.container.querySelectorAll('track')).map((t) => t.hasAttribute('default'))).toEqual([false, true]);
    expect(first.container.querySelector('track')?.getAttribute('label')).toBe('Finnish');
    first.unmount();

    spy.mockResolvedValue([{ index: 0, language: 'fin', title: 'Finnish', default: false }]);
    const second = render(<VideoPlayerPage source={{ kind: 'public', token: 'tok' }} />);
    await waitFor(() => expect(second.container.querySelectorAll('track').length).toBe(1));
    expect(second.container.querySelector('track')?.hasAttribute('default')).toBe(false);
  });

  it('still plays the video when the subtitle list cannot be loaded', async () => {
    vi.spyOn(api, 'getPublicVideoInfo').mockResolvedValue({ name: 'movie.mp4' });
    vi.spyOn(api, 'getVideoSubtitles').mockRejectedValue(new api.ApiError(500, 'boom'));
    const { container } = render(<VideoPlayerPage source={{ kind: 'public', token: 'tok' }} />);
    await waitFor(() => expect(container.querySelector('video')).not.toBeNull());
    expect(container.querySelectorAll('track').length).toBe(0);
  });
});
