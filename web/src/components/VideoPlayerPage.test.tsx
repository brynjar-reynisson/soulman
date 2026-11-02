import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { VideoPlayerPage } from './VideoPlayerPage';
import * as api from '../api';

vi.mock('../auth', () => ({ getAccessToken: async () => 'jwt' }));

beforeEach(() => {
  vi.restoreAllMocks();
});

describe('VideoPlayerPage', () => {
  it('public visitor sees the player and no back link or share button', async () => {
    vi.spyOn(api, 'getPublicVideoInfo').mockResolvedValue({ name: 'beach.mp4' });
    const { container } = render(<VideoPlayerPage source={{ kind: 'public', token: 'tok' }} />);
    await screen.findByText('beach.mp4');
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
});
