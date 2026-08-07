import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getObsidianFolders: vi.fn().mockResolvedValue({ folders: [] }) };
});

describe('ObsidianPage', () => {
  it('calls onBack when the back link is clicked', async () => {
    const onBack = vi.fn();
    const { ObsidianPage } = await import('./ObsidianPage');
    render(<ObsidianPage onBack={onBack} />);

    await userEvent.click(screen.getByText(/soulman/i));

    expect(onBack).toHaveBeenCalled();
  });
});
