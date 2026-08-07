import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockSaveObsidianFile = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, saveObsidianFile: (...args: unknown[]) => mockSaveObsidianFile(...args) };
});

beforeEach(() => vi.clearAllMocks());

describe('ObsidianFileEditor', () => {
  it('saves edited content and calls onSaved with the new value', async () => {
    mockSaveObsidianFile.mockResolvedValue(undefined);
    const onSaved = vi.fn();
    const { ObsidianFileEditor } = await import('./ObsidianFileEditor');
    render(
      <ObsidianFileEditor folder="soulman" file="NOTES.md" initialContent="old" onSaved={onSaved} onCancel={vi.fn()} />,
    );

    const textarea = screen.getByRole('textbox');
    await userEvent.clear(textarea);
    await userEvent.type(textarea, 'new content');
    await userEvent.click(screen.getByRole('button', { name: /save/i }));

    expect(mockSaveObsidianFile).toHaveBeenCalledWith('tok-abc', 'soulman', 'NOTES.md', 'new content');
    expect(onSaved).toHaveBeenCalledWith('new content');
  });

  it('calls onCancel without saving when the close button is clicked', async () => {
    const onCancel = vi.fn();
    const { ObsidianFileEditor } = await import('./ObsidianFileEditor');
    render(
      <ObsidianFileEditor folder="soulman" file="NOTES.md" initialContent="old" onSaved={vi.fn()} onCancel={onCancel} />,
    );

    await userEvent.click(screen.getByRole('button', { name: /close without saving/i }));

    expect(mockSaveObsidianFile).not.toHaveBeenCalled();
    expect(onCancel).toHaveBeenCalled();
  });

  it('shows an error and stays in edit mode when save fails', async () => {
    mockSaveObsidianFile.mockRejectedValue(new Error('network error'));
    const onSaved = vi.fn();
    const { ObsidianFileEditor } = await import('./ObsidianFileEditor');
    render(
      <ObsidianFileEditor folder="soulman" file="NOTES.md" initialContent="old" onSaved={onSaved} onCancel={vi.fn()} />,
    );

    await userEvent.click(screen.getByRole('button', { name: /save/i }));

    expect(await screen.findByText(/save failed/i)).toBeInTheDocument();
    expect(onSaved).not.toHaveBeenCalled();
  });
});
