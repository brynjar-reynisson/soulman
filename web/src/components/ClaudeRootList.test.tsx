import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetClaudeRoots = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getClaudeRoots: (...args: unknown[]) => mockGetClaudeRoots(...args) };
});

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, '', '/');
});

describe('ClaudeRootList', () => {
  it('lists existing roots and expands one to show its folders', async () => {
    mockGetClaudeRoots.mockResolvedValue({
      roots: [
        { label: 'Obsidian', path: 'C:\\obsidian', exists: true, folders: ['soulman'] },
        { label: 'IdeaProjects', path: 'C:\\IdeaProjects', exists: true, folders: ['digital-me'] },
      ],
    });
    const { ClaudeRootList } = await import('./ClaudeRootList');
    render(<ClaudeRootList />);

    await userEvent.click(await screen.findByText('Obsidian'));

    expect(await screen.findByText('soulman')).toBeInTheDocument();
    expect(screen.queryByText('digital-me')).not.toBeInTheDocument();
  });

  it('shows a missing root as unavailable and does not let it expand', async () => {
    mockGetClaudeRoots.mockResolvedValue({
      roots: [{ label: 'Misc Projects', path: 'C:\\misc_projects', exists: false, folders: [] }],
    });
    const { ClaudeRootList } = await import('./ClaudeRootList');
    render(<ClaudeRootList />);

    expect(await screen.findByText(/misc projects.*not found/i)).toBeInTheDocument();
  });

  it('opens the launch form for a selected folder, defaulted to the folder name', async () => {
    mockGetClaudeRoots.mockResolvedValue({
      roots: [{ label: 'Obsidian', path: 'C:\\obsidian', exists: true, folders: ['soulman'] }],
    });
    const { ClaudeRootList } = await import('./ClaudeRootList');
    render(<ClaudeRootList />);

    await userEvent.click(await screen.findByText('Obsidian'));
    await userEvent.click(await screen.findByText('soulman'));

    expect(screen.getByRole('textbox')).toHaveValue('soulman');
  });

  it('shows an error banner when roots fail to load', async () => {
    mockGetClaudeRoots.mockRejectedValue(new Error('network error'));
    const { ClaudeRootList } = await import('./ClaudeRootList');
    render(<ClaudeRootList />);

    expect(await screen.findByText(/roots unavailable/i)).toBeInTheDocument();
  });

  it('restores the previously expanded root from the URL on mount', async () => {
    window.history.replaceState(null, '', '/?claudeRoot=Obsidian');
    mockGetClaudeRoots.mockResolvedValue({
      roots: [{ label: 'Obsidian', path: 'C:\\obsidian', exists: true, folders: ['soulman'] }],
    });
    const { ClaudeRootList } = await import('./ClaudeRootList');
    render(<ClaudeRootList />);

    expect(await screen.findByText('soulman')).toBeInTheDocument();
  });
});
