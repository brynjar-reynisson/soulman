import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetObsidianFolders = vi.fn();
const mockGetObsidianFiles = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getObsidianFolders: (...args: unknown[]) => mockGetObsidianFolders(...args),
    getObsidianFiles: (...args: unknown[]) => mockGetObsidianFiles(...args),
  };
});

beforeEach(() => vi.clearAllMocks());

describe('ObsidianFolderList', () => {
  it('lists folders and expands one to show its files', async () => {
    mockGetObsidianFolders.mockResolvedValue({ folders: ['brynjar-obsidian', 'soulman'] });
    mockGetObsidianFiles.mockResolvedValue({ files: ['NOTES.md'] });
    const { ObsidianFolderList } = await import('./ObsidianFolderList');
    render(<ObsidianFolderList />);

    await userEvent.click(await screen.findByText('soulman'));

    expect(await screen.findByText('NOTES.md')).toBeInTheDocument();
  });

  it('collapses the previously open folder when a different one is selected', async () => {
    mockGetObsidianFolders.mockResolvedValue({ folders: ['brynjar-obsidian', 'soulman'] });
    mockGetObsidianFiles.mockResolvedValue({ files: ['NOTES.md'] });
    const { ObsidianFolderList } = await import('./ObsidianFolderList');
    render(<ObsidianFolderList />);

    await userEvent.click(await screen.findByText('soulman'));
    await screen.findByText('NOTES.md');
    mockGetObsidianFiles.mockResolvedValue({ files: ['diary.md'] });
    await userEvent.click(screen.getByText('brynjar-obsidian'));

    expect(await screen.findByText('diary.md')).toBeInTheDocument();
    expect(screen.queryByText('NOTES.md')).not.toBeInTheDocument();
    expect(mockGetObsidianFiles).toHaveBeenCalledTimes(2);
  });

  it('shows an error banner when folders fail to load', async () => {
    mockGetObsidianFolders.mockRejectedValue(new Error('network error'));
    const { ObsidianFolderList } = await import('./ObsidianFolderList');
    render(<ObsidianFolderList />);

    expect(await screen.findByText(/folders unavailable/i)).toBeInTheDocument();
  });
});
