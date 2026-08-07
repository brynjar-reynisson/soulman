import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetObsidianFiles = vi.fn();
const mockGetObsidianFile = vi.fn();
const mockCreateObsidianFile = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getObsidianFiles: (...args: unknown[]) => mockGetObsidianFiles(...args),
    getObsidianFile: (...args: unknown[]) => mockGetObsidianFile(...args),
    createObsidianFile: (...args: unknown[]) => mockCreateObsidianFile(...args),
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

describe('ObsidianFileList create', () => {
  it('creates a new file and selects it', async () => {
    mockGetObsidianFiles.mockResolvedValueOnce({ files: [] }).mockResolvedValueOnce({ files: ['new.md'] });
    mockCreateObsidianFile.mockResolvedValue(undefined);
    mockGetObsidianFile.mockResolvedValue({ content: '' });
    const { ObsidianFileList } = await import('./ObsidianFileList');
    render(<ObsidianFileList folder="soulman" />);

    await userEvent.click(await screen.findByText(/new file/i));
    await userEvent.type(screen.getByPlaceholderText('filename.md'), 'new.md');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));

    expect(mockCreateObsidianFile).toHaveBeenCalledWith('tok-abc', 'soulman', 'new.md', '');
    expect(screen.getByRole('button', { name: 'new.md' })).toBeInTheDocument();
  });

  it('shows an error when create fails', async () => {
    mockGetObsidianFiles.mockResolvedValue({ files: [] });
    mockCreateObsidianFile.mockRejectedValue(new Error('conflict'));
    const { ObsidianFileList } = await import('./ObsidianFileList');
    render(<ObsidianFileList folder="soulman" />);

    await userEvent.click(await screen.findByText(/new file/i));
    await userEvent.type(screen.getByPlaceholderText('filename.md'), 'dup.md');
    await userEvent.click(screen.getByRole('button', { name: /^create$/i }));

    expect(await screen.findByText(/could not create file/i)).toBeInTheDocument();
  });
});
