import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { RawInputModal } from './RawInputModal';
import type { RawInput } from '../api';

const baseInput: RawInput = {
  stimulus_id: 'stim-1',
  received_at: '2026-07-20T00:00:00Z',
  channel: 'cli-note',
  normalized_text: 'hello world',
  raw_payload: { foo: 'bar' },
};

describe('RawInputModal', () => {
  it('renders the full record including pretty-printed raw_payload', () => {
    render(<RawInputModal input={baseInput} onClose={vi.fn()} />);
    expect(screen.getByText('stim-1')).toBeInTheDocument();
    expect(screen.getByText('cli-note')).toBeInTheDocument();
    expect(screen.getByText('2026-07-20T00:00:00Z')).toBeInTheDocument();
    expect(screen.getByText(/"foo": "bar"/)).toBeInTheDocument();
  });

  it('omits the override command row when absent', () => {
    render(<RawInputModal input={baseInput} onClose={vi.fn()} />);
    expect(screen.queryByText('Override Command')).not.toBeInTheDocument();
  });

  it('shows the override command row when present', () => {
    render(<RawInputModal input={{ ...baseInput, override_cmd: 'PAUSE' }} onClose={vi.fn()} />);
    expect(screen.getByText('Override Command')).toBeInTheDocument();
    expect(screen.getByText('PAUSE')).toBeInTheDocument();
  });

  it('calls onClose when the close button is clicked', () => {
    const onClose = vi.fn();
    render(<RawInputModal input={baseInput} onClose={onClose} />);
    fireEvent.click(screen.getByLabelText('Close'));
    expect(onClose).toHaveBeenCalled();
  });

  it('calls onClose when the backdrop is clicked', () => {
    const onClose = vi.fn();
    const { container } = render(<RawInputModal input={baseInput} onClose={onClose} />);
    fireEvent.click(container.firstChild as Element);
    expect(onClose).toHaveBeenCalled();
  });

  it('does not close when clicking inside the card', () => {
    const onClose = vi.fn();
    render(<RawInputModal input={baseInput} onClose={onClose} />);
    fireEvent.click(screen.getByText('Raw Input'));
    expect(onClose).not.toHaveBeenCalled();
  });

  it('calls onClose on Escape keydown', () => {
    const onClose = vi.fn();
    render(<RawInputModal input={baseInput} onClose={onClose} />);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalled();
  });
});
