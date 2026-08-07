import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetObsidianFile = vi.fn();
const mockSaveObsidianFile = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getObsidianFile: (...args: unknown[]) => mockGetObsidianFile(...args),
    saveObsidianFile: (...args: unknown[]) => mockSaveObsidianFile(...args),
  };
});

beforeEach(() => vi.clearAllMocks());

describe('ObsidianFileViewer', () => {
  it('renders markdown content for a .md file', async () => {
    mockGetObsidianFile.mockResolvedValue({ content: '# Heading' });
    const { ObsidianFileViewer } = await import('./ObsidianFileViewer');
    render(<ObsidianFileViewer folder="soulman" file="NOTES.md" />);

    expect(await screen.findByRole('heading', { name: 'Heading' })).toBeInTheDocument();
  });

  it('renders plain text content for a .txt file', async () => {
    mockGetObsidianFile.mockResolvedValue({ content: 'plain text here' });
    const { ObsidianFileViewer } = await import('./ObsidianFileViewer');
    render(<ObsidianFileViewer folder="soulman" file="todo.txt" />);

    expect(await screen.findByText('plain text here')).toBeInTheDocument();
  });

  it('shows an error banner when the fetch fails', async () => {
    mockGetObsidianFile.mockRejectedValue(new Error('network error'));
    const { ObsidianFileViewer } = await import('./ObsidianFileViewer');
    render(<ObsidianFileViewer folder="soulman" file="NOTES.md" />);

    expect(await screen.findByText(/file unavailable/i)).toBeInTheDocument();
  });

  it('switches to the editor when the edit button is clicked, and back on cancel', async () => {
    mockGetObsidianFile.mockResolvedValue({ content: 'hello' });
    const { ObsidianFileViewer } = await import('./ObsidianFileViewer');
    render(<ObsidianFileViewer folder="soulman" file="todo.txt" />);

    await screen.findByText('hello');
    await userEvent.click(screen.getByRole('button', { name: /edit/i }));

    expect(screen.getByRole('textbox')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /close without saving/i }));

    expect(await screen.findByText('hello')).toBeInTheDocument();
  });
});
