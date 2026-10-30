import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ApiError } from '../api';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockTree = vi.fn();
const mockEntries = vi.fn();
const mockCreate = vi.fn();
const mockSave = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getDiaryTree: (...a: unknown[]) => mockTree(...a),
    getDiaryEntries: (...a: unknown[]) => mockEntries(...a),
    createDiaryEntry: (...a: unknown[]) => mockCreate(...a),
    saveDiaryEntry: (...a: unknown[]) => mockSave(...a),
  };
});

const tree = {
  years: [
    {
      year: '2026',
      months: [
        { month: '03', days: [{ day: '05', count: 2 }, { day: '04', count: 1 }] },
        { month: '02', days: [{ day: '01', count: 1 }] },
      ],
    },
    { year: '2025', months: [{ month: '12', days: [{ day: '31', count: 1 }] }] },
  ],
};

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, '', '/?page=diary');
  mockTree.mockResolvedValue(tree);
  mockEntries.mockResolvedValue({
    entries: [
      { name: 'a.txt', time: '09:15', content: 'first entry' },
      { name: 'b.txt', time: '18:30', content: 'second entry' },
    ],
  });
});

async function renderPage(onBack = vi.fn()) {
  const { DiaryPage } = await import('./DiaryPage');
  render(<DiaryPage onBack={onBack} />);
  return onBack;
}

describe('DiaryPage', () => {
  it('shows years newest first with entry counts', async () => {
    await renderPage();
    const y2026 = await screen.findByRole('button', { name: /2026/ });
    const y2025 = screen.getByRole('button', { name: /2025/ });
    expect(y2026.compareDocumentPosition(y2025) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(y2026).toHaveTextContent('4');
    expect(y2025).toHaveTextContent('1');
  });

  it('opening a month name shows its days ("March" not "03")', async () => {
    await renderPage();
    await userEvent.click(await screen.findByRole('button', { name: /2026/ }));
    expect(screen.getByRole('button', { name: /March/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^\W*03/ })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /March/ }));
    expect(screen.getByRole('button', { name: /^\W*05/ })).toBeInTheDocument();
    expect(window.location.search).toContain('diaryMonth=03');
  });

  it('opening a year closes the other year and resets the open month', async () => {
    await renderPage();
    await userEvent.click(await screen.findByRole('button', { name: /2026/ }));
    await userEvent.click(screen.getByRole('button', { name: /March/ }));
    await userEvent.click(screen.getByRole('button', { name: /2025/ }));
    expect(screen.queryByRole('button', { name: /March/ })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /December/ })).toBeInTheDocument();
    expect(window.location.search).not.toContain('diaryMonth=03');
    await userEvent.click(screen.getByRole('button', { name: /2026/ }));
    expect(screen.getByRole('button', { name: /March/ })).toHaveAttribute('aria-expanded', 'false');
  });

  it('opening a day loads and shows all entries in time order, each with its time', async () => {
    await renderPage();
    await userEvent.click(await screen.findByRole('button', { name: /2026/ }));
    await userEvent.click(screen.getByRole('button', { name: /March/ }));
    await userEvent.click(screen.getByRole('button', { name: /^\W*05/ }));
    expect(await screen.findByText('first entry')).toBeInTheDocument();
    expect(mockEntries).toHaveBeenCalledWith('tok-abc', '2026-03-05');
    expect(screen.getByText('09:15')).toBeInTheDocument();
    expect(screen.getByText('18:30')).toBeInTheDocument();
    const a = screen.getByText('first entry');
    const b = screen.getByText('second entry');
    expect(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('restores open year/month/day from diaryYear/diaryMonth/diaryDay URL params', async () => {
    window.history.replaceState(null, '', '/?page=diary&diaryYear=2026&diaryMonth=03&diaryDay=05');
    await renderPage();
    expect(await screen.findByText('first entry')).toBeInTheDocument();
    expect(mockEntries).toHaveBeenCalledWith('tok-abc', '2026-03-05');
  });

  it("ignores a diaryYear param that isn't in the tree", async () => {
    window.history.replaceState(null, '', '/?page=diary&diaryYear=1999&diaryMonth=01&diaryDay=01');
    await renderPage();
    expect(await screen.findByRole('button', { name: /2026/ })).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByRole('button', { name: /2025/ })).toHaveAttribute('aria-expanded', 'false');
    expect(mockEntries).not.toHaveBeenCalled();
  });

  it('save new entry calls createDiaryEntry, refreshes the tree and opens today', async () => {
    mockCreate.mockResolvedValue({ date: '2026-03-05', name: 'c.txt', time: '20:00' });
    await renderPage();
    await screen.findByRole('button', { name: /2026/ });
    await userEvent.type(screen.getByLabelText('New entry'), 'dear diary');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(mockCreate).toHaveBeenCalledWith('tok-abc', 'dear diary');
    expect(await screen.findByText('first entry')).toBeInTheDocument();
    expect(mockTree).toHaveBeenCalledTimes(2);
    expect(mockEntries).toHaveBeenCalledWith('tok-abc', '2026-03-05');
    expect(window.location.search).toContain('diaryYear=2026');
    expect(window.location.search).toContain('diaryDay=05');
    expect(screen.getByLabelText('New entry')).toHaveValue('');
  });

  it('Enter in the new-entry box saves the entry and does not insert a newline', async () => {
    mockCreate.mockResolvedValue({ date: '2026-03-05', name: 'c.txt', time: '20:00' });
    await renderPage();
    await screen.findByRole('button', { name: /2026/ });
    await userEvent.type(screen.getByLabelText('New entry'), 'dear diary{Enter}');
    expect(mockCreate).toHaveBeenCalledTimes(1);
    expect(mockCreate).toHaveBeenCalledWith('tok-abc', 'dear diary');
  });

  it('Shift+Enter inserts a newline and does not save', async () => {
    await renderPage();
    await screen.findByRole('button', { name: /2026/ });
    const box = screen.getByLabelText('New entry');
    await userEvent.type(box, 'line one{Shift>}{Enter}{/Shift}line two');
    expect(box).toHaveValue('line one\nline two');
    expect(mockCreate).not.toHaveBeenCalled();
  });

  it('Enter on blank text does not save and does not add a newline', async () => {
    await renderPage();
    await screen.findByRole('button', { name: /2026/ });
    const box = screen.getByLabelText('New entry');
    await userEvent.type(box, '  {Enter}');
    expect(mockCreate).not.toHaveBeenCalled();
    expect(box).toHaveValue('  ');
  });

  it('Save is disabled while the new-entry text is blank', async () => {
    await renderPage();
    await screen.findByRole('button', { name: /2026/ });
    const save = screen.getByRole('button', { name: 'Save' });
    expect(save).toBeDisabled();
    await userEvent.type(screen.getByLabelText('New entry'), '   ');
    expect(save).toBeDisabled();
    await userEvent.type(screen.getByLabelText('New entry'), 'x');
    expect(save).toBeEnabled();
  });

  it('Edit swaps to a textarea; Save calls saveDiaryEntry and shows the new text; Cancel restores the old text', async () => {
    window.history.replaceState(null, '', '/?page=diary&diaryYear=2026&diaryMonth=03&diaryDay=05');
    mockSave.mockResolvedValue(undefined);
    await renderPage();
    await screen.findByText('first entry');

    await userEvent.click(screen.getAllByRole('button', { name: 'Edit' })[0]);
    const ta = screen.getByLabelText('Edit entry');
    expect(ta).toHaveValue('first entry');
    await userEvent.clear(ta);
    await userEvent.type(ta, 'changed');
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.getByText('first entry')).toBeInTheDocument();
    expect(mockSave).not.toHaveBeenCalled();

    await userEvent.click(screen.getAllByRole('button', { name: 'Edit' })[0]);
    const ta2 = screen.getByLabelText('Edit entry');
    await userEvent.clear(ta2);
    await userEvent.type(ta2, 'rewritten');
    const row = ta2.closest('li') as HTMLElement;
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }));
    expect(mockSave).toHaveBeenCalledWith('tok-abc', '2026-03-05', 'a.txt', 'rewritten');
    expect(await screen.findByText('rewritten')).toBeInTheDocument();
  });

  it('shows the unreachable message when the tree call fails with ApiError 503', async () => {
    mockTree.mockRejectedValue(new ApiError(503, 'x'));
    await renderPage();
    expect(
      await screen.findByText("Diary folder isn't reachable — is Google Drive running?"),
    ).toBeInTheDocument();
  });

  it('edit save failure keeps the draft and error; a retry that succeeds clears the error', async () => {
    window.history.replaceState(null, '', '/?page=diary&diaryYear=2026&diaryMonth=03&diaryDay=05');
    mockSave.mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce(undefined);
    await renderPage();
    await screen.findByText('first entry');
    await userEvent.click(screen.getAllByRole('button', { name: 'Edit' })[0]);
    const ta = screen.getByLabelText('Edit entry');
    await userEvent.clear(ta);
    await userEvent.type(ta, 'rewritten');
    const row = ta.closest('li') as HTMLElement;
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Save failed')).toBeInTheDocument();
    expect(screen.getByLabelText('Edit entry')).toHaveValue('rewritten');
    await userEvent.click(within(row).getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('rewritten')).toBeInTheDocument();
    expect(screen.queryByText('Save failed')).not.toBeInTheDocument();
  });

  it('a failed create keeps the new-entry text', async () => {
    mockCreate.mockRejectedValue(new Error('boom'));
    await renderPage();
    await screen.findByRole('button', { name: /2026/ });
    await userEvent.type(screen.getByLabelText('New entry'), 'dear diary');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Save failed')).toBeInTheDocument();
    expect(screen.getByLabelText('New entry')).toHaveValue('dear diary');
  });

  it('an in-flight edit save resolving after switching day does not touch the other day', async () => {
    window.history.replaceState(null, '', '/?page=diary&diaryYear=2026&diaryMonth=03&diaryDay=05');
    let resolveSave: () => void = () => {};
    mockSave.mockReturnValue(new Promise<void>((res) => { resolveSave = res; }));
    mockEntries.mockImplementation(async (_t: string, d: string) =>
      d === '2026-03-05'
        ? { entries: [{ name: 'a.txt', time: '09:15', content: 'first entry' }] }
        : { entries: [{ name: 'a.txt', time: '08:00', content: 'other day entry' }] },
    );
    await renderPage();
    await screen.findByText('first entry');
    await userEvent.click(screen.getAllByRole('button', { name: 'Edit' })[0]);
    const ta = screen.getByLabelText('Edit entry');
    await userEvent.clear(ta);
    await userEvent.type(ta, 'rewritten');
    await userEvent.click(within(ta.closest('li') as HTMLElement).getByRole('button', { name: 'Save' }));
    await userEvent.click(screen.getByRole('button', { name: /^\W*04/ }));
    expect(await screen.findByText('other day entry')).toBeInTheDocument();
    resolveSave();
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.getByText('other day entry')).toBeInTheDocument();
    expect(screen.queryByText('rewritten')).not.toBeInTheDocument();
  });

  it('a failed entries fetch shows the error, not Loading', async () => {
    window.history.replaceState(null, '', '/?page=diary&diaryYear=2026&diaryMonth=03&diaryDay=05');
    mockEntries.mockRejectedValue(new Error('boom'));
    await renderPage();
    expect(await screen.findByText('Entries unavailable')).toBeInTheDocument();
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
  });

  it('Back calls onBack', async () => {
    const onBack = await renderPage();
    await userEvent.click(screen.getByRole('button', { name: /Soulman/ }));
    expect(onBack).toHaveBeenCalled();
  });
});
