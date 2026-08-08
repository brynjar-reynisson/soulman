import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getObsidianFolders: vi.fn().mockResolvedValue({ folders: [] }) };
});

describe('ObsidianPage', () => {
  beforeEach(() => window.history.replaceState(null, '', '/'));

  it('calls onBack when the back link is clicked', async () => {
    const onBack = vi.fn();
    const { ObsidianPage } = await import('./ObsidianPage');
    render(<ObsidianPage onBack={onBack} />);

    await userEvent.click(screen.getByText(/soulman/i));

    expect(onBack).toHaveBeenCalled();
  });
});
