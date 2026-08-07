import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetObsidianFiles = vi.fn();
const mockGetObsidianFile = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getObsidianFiles: (...args: unknown[]) => mockGetObsidianFiles(...args),
    getObsidianFile: (...args: unknown[]) => mockGetObsidianFile(...args),
  };
});

beforeEach(() => vi.clearAllMocks());

describe('ObsidianFileList', () => {
  it('lists files for the given folder', async () => {
    mockGetObsidianFiles.mockResolvedValue({ files: ['NOTES.md', 'todo.txt'] });
    const { ObsidianFileList } = await import('./ObsidianFileList');
    render(<ObsidianFileList folder="soulman" />);

    expect(await screen.findByText('NOTES.md')).toBeInTheDocument();
    expect(screen.getByText('todo.txt')).toBeInTheDocument();
  });

  it('shows the file viewer when a file is selected', async () => {
    mockGetObsidianFiles.mockResolvedValue({ files: ['NOTES.md'] });
    mockGetObsidianFile.mockResolvedValue({ content: 'hello' });
    const { ObsidianFileList } = await import('./ObsidianFileList');
    render(<ObsidianFileList folder="soulman" />);

    await userEvent.click(await screen.findByText('NOTES.md'));

    expect(await screen.findByText('hello')).toBeInTheDocument();
  });

  it('shows an empty state when the folder has no files', async () => {
    mockGetObsidianFiles.mockResolvedValue({ files: [] });
    const { ObsidianFileList } = await import('./ObsidianFileList');
    render(<ObsidianFileList folder="soulman" />);

    expect(await screen.findByText(/no files/i)).toBeInTheDocument();
  });
});
