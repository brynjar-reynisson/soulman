import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Dashboard } from './Dashboard';

vi.mock('./StatusPanel', () => ({ StatusPanel: () => null }));
vi.mock('./EpisodesPanel', () => ({ EpisodesPanel: () => null }));
vi.mock('./RawInputsPanel', () => ({ RawInputsPanel: () => null }));
vi.mock('./ReportsPanel', () => ({ ReportsPanel: () => null }));

describe('Dashboard', () => {
  it('links are alphabetical with Sign out last, and Diary calls onOpenDiary', async () => {
    const onOpenDiary = vi.fn();
    render(
      <Dashboard
        initialStatus={null}
        onSignOut={vi.fn()}
        onOpenObsidian={vi.fn()}
        onOpenClaude={vi.fn()}
        onOpenFiles={vi.fn()}
        onOpenSearch={vi.fn()}
        onOpenProjects={vi.fn()}
        onOpenDiary={onOpenDiary}
        onOpenVideos={vi.fn()}
      />,
    );
    const names = screen.getAllByRole('button').map((b) => b.textContent);
    expect(names).toEqual(['Claude', 'Diary', 'Files', 'Obsidian', 'Projects', 'Videos', 'Web-Search', 'Sign out']);
    await userEvent.click(screen.getByRole('button', { name: 'Diary' }));
    expect(onOpenDiary).toHaveBeenCalled();
  });
});
