import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetRawInputs = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getRawInputs: (...args: unknown[]) => mockGetRawInputs(...args) };
});

beforeEach(() => vi.clearAllMocks());

const sampleInput = {
  stimulus_id: 'stim-1',
  received_at: '2026-07-20T00:00:00Z',
  channel: 'cli-note',
  normalized_text: 'hello world',
  raw_payload: { foo: 'bar' },
};

describe('RawInputsPanel', () => {
  it('shows raw inputs once loaded, with line-clamp applied to the text', async () => {
    mockGetRawInputs.mockResolvedValue([sampleInput]);
    const { RawInputsPanel } = await import('./RawInputsPanel');
    render(<RawInputsPanel />);

    const text = await screen.findByText('hello world');
    expect(text).toHaveClass('line-clamp-2');
  });

  it('shows an error banner without throwing when the fetch fails', async () => {
    mockGetRawInputs.mockRejectedValue(new Error('network error'));
    const { RawInputsPanel } = await import('./RawInputsPanel');
    render(<RawInputsPanel />);

    expect(await screen.findByText(/raw inputs unavailable/i)).toBeInTheDocument();
  });

  it('shows an empty state when there are no raw inputs', async () => {
    mockGetRawInputs.mockResolvedValue([]);
    const { RawInputsPanel } = await import('./RawInputsPanel');
    render(<RawInputsPanel />);

    expect(await screen.findByText(/no raw inputs yet/i)).toBeInTheDocument();
  });

  it('opens the detail modal when an entry is clicked, showing its raw_payload', async () => {
    mockGetRawInputs.mockResolvedValue([sampleInput]);
    const { RawInputsPanel } = await import('./RawInputsPanel');
    render(<RawInputsPanel />);

    const row = await screen.findByText('hello world');
    fireEvent.click(row);

    expect(await screen.findByText(/"foo": "bar"/)).toBeInTheDocument();
  });

  it('closes the modal when its close button is clicked', async () => {
    mockGetRawInputs.mockResolvedValue([sampleInput]);
    const { RawInputsPanel } = await import('./RawInputsPanel');
    render(<RawInputsPanel />);

    fireEvent.click(await screen.findByText('hello world'));
    fireEvent.click(await screen.findByLabelText('Close'));

    expect(screen.queryByText(/"foo": "bar"/)).not.toBeInTheDocument();
  });
});
