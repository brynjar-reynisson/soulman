import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetLatestReport = vi.fn();
const mockGetReportByDate = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getLatestReport: (...args: unknown[]) => mockGetLatestReport(...args),
    getReportByDate: (...args: unknown[]) => mockGetReportByDate(...args),
  };
});

beforeEach(() => vi.clearAllMocks());

describe('ReportsPanel', () => {
  it('shows the latest report on load', async () => {
    mockGetLatestReport.mockResolvedValue({ date: '2026-07-19', content: 'latest report content' });
    const { ReportsPanel } = await import('./ReportsPanel');
    render(<ReportsPanel />);

    expect(await screen.findByText(/latest report content/i)).toBeInTheDocument();
  });

  it('loads a specific date report when the date picker changes', async () => {
    mockGetLatestReport.mockResolvedValue({ date: '2026-07-19', content: 'latest' });
    mockGetReportByDate.mockResolvedValue({ date: '2026-06-01', content: 'june first content' });
    const { ReportsPanel } = await import('./ReportsPanel');
    render(<ReportsPanel />);
    await screen.findByText(/latest/i);

    const input = screen.getByLabelText(/report date/i, { selector: 'input' }) as HTMLInputElement;
    fireEvent.change(input, { target: { value: '2026-06-01' } });

    expect(await screen.findByText(/june first content/i)).toBeInTheDocument();
    expect(mockGetReportByDate).toHaveBeenCalledWith('tok-abc', '2026-06-01');
  });

  it('shows an error message when no report exists for the selected date', async () => {
    mockGetLatestReport.mockResolvedValue({ date: '2026-07-19', content: 'latest' });
    mockGetReportByDate.mockRejectedValue(new Error('not found'));
    const { ReportsPanel } = await import('./ReportsPanel');
    render(<ReportsPanel />);
    await screen.findByText(/latest/i);

    const input = screen.getByLabelText(/report date/i, { selector: 'input' });
    await userEvent.type(input, '2020-01-01');

    expect(await screen.findByText(/no report for this date/i)).toBeInTheDocument();
  });

  it('ignores a stale response when the date changes again before the first fetch resolves', async () => {
    mockGetLatestReport.mockResolvedValue({ date: '2026-07-19', content: 'latest' });

    let resolveFirst: (value: { date: string; content: string }) => void;
    const firstPromise = new Promise<{ date: string; content: string }>((resolve) => {
      resolveFirst = resolve;
    });
    mockGetReportByDate
      .mockImplementationOnce(() => firstPromise)
      .mockImplementationOnce(() => Promise.resolve({ date: '2026-06-01', content: 'june first content' }));

    const { ReportsPanel } = await import('./ReportsPanel');
    render(<ReportsPanel />);
    await screen.findByText(/latest/i);

    const input = screen.getByLabelText(/report date/i, { selector: 'input' });
    fireEvent.change(input, { target: { value: '2026-01-01' } }); // slow request, in flight
    fireEvent.change(input, { target: { value: '2026-06-01' } }); // fast request, resolves first

    expect(await screen.findByText(/june first content/i)).toBeInTheDocument();

    // Now let the slow (stale) first request resolve — it must NOT overwrite June's content.
    await act(async () => {
      resolveFirst!({ date: '2026-01-01', content: 'january content' });
      await Promise.resolve();
    });

    expect(screen.getByText(/june first content/i)).toBeInTheDocument();
    expect(screen.queryByText(/january content/i)).not.toBeInTheDocument();
  });
});
