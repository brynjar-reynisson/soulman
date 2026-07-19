import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetEpisodes = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getEpisodes: (...args: unknown[]) => mockGetEpisodes(...args) };
});

beforeEach(() => vi.clearAllMocks());

describe('EpisodesPanel', () => {
  it('shows episodes once loaded', async () => {
    mockGetEpisodes.mockResolvedValue([
      { id: 1, occurred_at: '2026-07-19T09:00:00Z', summary: 'Disk space critical' },
    ]);
    const { EpisodesPanel } = await import('./EpisodesPanel');
    render(<EpisodesPanel />);

    expect(await screen.findByText(/disk space critical/i)).toBeInTheDocument();
  });

  it('shows an error banner without throwing when the fetch fails', async () => {
    mockGetEpisodes.mockRejectedValue(new Error('network error'));
    const { EpisodesPanel } = await import('./EpisodesPanel');
    render(<EpisodesPanel />);

    expect(await screen.findByText(/episodes unavailable/i)).toBeInTheDocument();
  });

  it('shows an empty state when there are no episodes', async () => {
    mockGetEpisodes.mockResolvedValue([]);
    const { EpisodesPanel } = await import('./EpisodesPanel');
    render(<EpisodesPanel />);

    expect(await screen.findByText(/no episodes yet/i)).toBeInTheDocument();
  });
});
