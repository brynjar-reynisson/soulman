import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Dashboard } from './Dashboard';

vi.mock('./StatusPanel', () => ({ StatusPanel: () => null }));
vi.mock('./EpisodesPanel', () => ({ EpisodesPanel: () => null }));
vi.mock('./RawInputsPanel', () => ({ RawInputsPanel: () => null }));
vi.mock('./ReportsPanel', () => ({ ReportsPanel: () => null }));

describe('Dashboard', () => {
  it('Diary link appears right after Projects and calls onOpenDiary', async () => {
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
      />,
    );
    const names = screen.getAllByRole('button').map((b) => b.textContent);
    expect(names.indexOf('Diary')).toBe(names.indexOf('Projects') + 1);
    await userEvent.click(screen.getByRole('button', { name: 'Diary' }));
    expect(onOpenDiary).toHaveBeenCalled();
  });
});
