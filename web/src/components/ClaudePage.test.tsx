import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getClaudeRoots: vi.fn().mockResolvedValue({ roots: [] }) };
});

describe('ClaudePage', () => {
  it('calls onBack when the back link is clicked', async () => {
    const onBack = vi.fn();
    const { ClaudePage } = await import('./ClaudePage');
    render(<ClaudePage onBack={onBack} />);

    await userEvent.click(screen.getByText(/soulman/i));

    expect(onBack).toHaveBeenCalled();
  });
});
