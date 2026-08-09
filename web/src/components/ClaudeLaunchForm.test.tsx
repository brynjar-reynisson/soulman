import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockLaunchClaudeSession = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, launchClaudeSession: (...args: unknown[]) => mockLaunchClaudeSession(...args) };
});

beforeEach(() => vi.clearAllMocks());

describe('ClaudeLaunchForm', () => {
  it('defaults the session name input to the folder name', async () => {
    const { ClaudeLaunchForm } = await import('./ClaudeLaunchForm');
    render(<ClaudeLaunchForm root="Obsidian" folder="soulman" />);

    expect(screen.getByRole('textbox')).toHaveValue('soulman');
  });

  it('launches with the edited session name and shows a success message', async () => {
    mockLaunchClaudeSession.mockResolvedValue(undefined);
    const { ClaudeLaunchForm } = await import('./ClaudeLaunchForm');
    render(<ClaudeLaunchForm root="Obsidian" folder="soulman" />);

    await userEvent.clear(screen.getByRole('textbox'));
    await userEvent.type(screen.getByRole('textbox'), 'my-session');
    await userEvent.click(screen.getByRole('button', { name: /launch/i }));

    expect(mockLaunchClaudeSession).toHaveBeenCalledWith('tok-abc', 'Obsidian', 'soulman', 'my-session');
    expect(await screen.findByText(/session 'my-session' launched/i)).toBeInTheDocument();
  });

  it('shows an error message when launch fails', async () => {
    const { ApiError } = await import('../api');
    mockLaunchClaudeSession.mockRejectedValue(new ApiError(500, 'launch failed'));
    const { ClaudeLaunchForm } = await import('./ClaudeLaunchForm');
    render(<ClaudeLaunchForm root="Obsidian" folder="soulman" />);

    await userEvent.click(screen.getByRole('button', { name: /launch/i }));

    expect(await screen.findByText(/launch failed/i)).toBeInTheDocument();
  });

  it('resets the input to the new folder name when the folder prop changes', async () => {
    const { ClaudeLaunchForm } = await import('./ClaudeLaunchForm');
    const { rerender } = render(<ClaudeLaunchForm root="Obsidian" folder="soulman" />);

    rerender(<ClaudeLaunchForm root="Obsidian" folder="other-project" />);

    expect(screen.getByRole('textbox')).toHaveValue('other-project');
  });
});
