// web/src/components/FileBrowser.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ApiError } from '../api';
import { setParams } from '../urlState';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockListFiles = vi.fn();
const mockDownloadFile = vi.fn();
const mockUploadFile = vi.fn();
const mockShareFile = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    listFiles: (...args: unknown[]) => mockListFiles(...args),
    downloadFile: (...args: unknown[]) => mockDownloadFile(...args),
    uploadFile: (...args: unknown[]) => mockUploadFile(...args),
    shareFile: (...args: unknown[]) => mockShareFile(...args),
  };
});

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, '', '/');
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
    configurable: true,
  });
});

describe('FileBrowser', () => {
  it('lists folders and files, and drills into a subfolder via breadcrumb', async () => {
    mockListFiles
      .mockResolvedValueOnce({ folders: ['Taxes'], files: [{ name: 'note.txt', size: 42 }] })
      .mockResolvedValueOnce({ folders: [], files: [{ name: '2025-return.pdf', size: 1024 }] });
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    await userEvent.click(await screen.findByText('Taxes'));

    expect(await screen.findByText('2025-return.pdf')).toBeInTheDocument();
    expect(mockListFiles).toHaveBeenLastCalledWith('tok-abc', 'Documents', 'Taxes');
    expect(screen.getByText('Documents')).toBeInTheDocument();
  });

  it('downloads a file when its Download button is clicked', async () => {
    mockListFiles.mockResolvedValue({ folders: [], files: [{ name: 'note.txt', size: 42 }] });
    mockDownloadFile.mockResolvedValue(undefined);
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    await userEvent.click(await screen.findByRole('button', { name: 'Download' }));

    expect(mockDownloadFile).toHaveBeenCalledWith('tok-abc', 'Documents', '', 'note.txt');
  });

  it('creates a share link, copies it to the clipboard, and shows a success message', async () => {
    mockListFiles.mockResolvedValue({ folders: [], files: [{ name: 'note.txt', size: 42 }] });
    mockShareFile.mockResolvedValue({ url: '/dl/abc123', expiresAt: '2026-08-19T16:00:00Z' });
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    await userEvent.click(await screen.findByRole('button', { name: 'Share' }));

    expect(mockShareFile).toHaveBeenCalledWith('tok-abc', 'Documents', '', 'note.txt');
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(`${window.location.origin}/dl/abc123`);
    expect(await screen.findByText('Link copied')).toBeInTheDocument();
  });

  it('shows an error and no success message when creating a share link fails', async () => {
    mockListFiles.mockResolvedValue({ folders: [], files: [{ name: 'note.txt', size: 42 }] });
    mockShareFile.mockRejectedValue(new ApiError(500, 'share failed (500)'));
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    await userEvent.click(await screen.findByRole('button', { name: 'Share' }));

    expect(await screen.findByText('Failed to create share link')).toBeInTheDocument();
    expect(screen.queryByText('Link copied')).not.toBeInTheDocument();
  });

  it('shows a replace confirmation on a 409 and retries with overwrite=true', async () => {
    mockListFiles.mockResolvedValue({ folders: [], files: [] });
    mockUploadFile.mockRejectedValueOnce(new ApiError(409, 'upload failed (409)'));
    mockUploadFile.mockResolvedValueOnce(undefined);
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    const file = new File(['hello'], 'note.txt', { type: 'text/plain' });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await userEvent.upload(input, file);
    await userEvent.click(await screen.findByText('Upload'));

    expect(await screen.findByText(/already exists/i)).toBeInTheDocument();

    await userEvent.click(screen.getByText('replace?'));

    expect(mockUploadFile).toHaveBeenLastCalledWith('tok-abc', 'Documents', '', file, true);
  });

  it('clicking a breadcrumb segment truncates the path to that depth', async () => {
    mockListFiles
      .mockResolvedValueOnce({ folders: ['Taxes'], files: [] })
      .mockResolvedValueOnce({ folders: ['2025'], files: [] })
      .mockResolvedValueOnce({ folders: [], files: [{ name: 'deep.txt', size: 1 }] })
      .mockResolvedValueOnce({ folders: ['2025'], files: [] });
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    await userEvent.click(await screen.findByText('Taxes'));
    await userEvent.click(await screen.findByText('2025'));
    await screen.findByText('deep.txt');
    expect(mockListFiles).toHaveBeenLastCalledWith('tok-abc', 'Documents', 'Taxes/2025');

    await userEvent.click(screen.getByText('Taxes'));

    expect(await screen.findByText('2025')).toBeInTheDocument();
    expect(mockListFiles).toHaveBeenLastCalledWith('tok-abc', 'Documents', 'Taxes');
  });

  it('shows a success message and resets the file input after a successful upload', async () => {
    mockListFiles.mockResolvedValue({ folders: [], files: [] });
    mockUploadFile.mockResolvedValueOnce(undefined);
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    const file = new File(['hello'], 'note.txt', { type: 'text/plain' });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await userEvent.upload(input, file);
    await userEvent.click(await screen.findByText('Upload'));

    expect(await screen.findByText(/"note.txt" uploaded successfully/i)).toBeInTheDocument();
    expect(input.value).toBe('');
  });

  it('clears a stale success message once a new file is chosen', async () => {
    mockListFiles.mockResolvedValue({ folders: [], files: [] });
    mockUploadFile.mockResolvedValueOnce(undefined);
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    const file = new File(['hello'], 'note.txt', { type: 'text/plain' });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await userEvent.upload(input, file);
    await userEvent.click(await screen.findByText('Upload'));
    await screen.findByText(/"note.txt" uploaded successfully/i);

    await userEvent.upload(input, new File(['x'], 'other.txt', { type: 'text/plain' }));

    expect(screen.queryByText(/uploaded successfully/i)).not.toBeInTheDocument();
  });

  it('resets currentPath when remounted with a different root (simulates a root switch via key change)', async () => {
    mockListFiles
      .mockResolvedValueOnce({ folders: ['Taxes'], files: [] })
      .mockResolvedValueOnce({ folders: [], files: [{ name: 'x.txt', size: 1 }] })
      .mockResolvedValueOnce({ folders: [], files: [{ name: 'y.txt', size: 2 }] });
    const { FileBrowser } = await import('./FileBrowser');
    const { rerender } = render(<FileBrowser key="Documents" root="Documents" />);
    await userEvent.click(await screen.findByText('Taxes'));
    await screen.findByText('x.txt');

    // Mirrors what FileRootList's root-switch handler actually does before
    // the remount: clear filePath in the URL so the fresh instance's lazy
    // currentPath initializer doesn't pick up the previous root's drilled-down
    // path. Without this, the URL (a jsdom global that outlives `rerender`)
    // would still carry the old filePath, defeating the point of the test.
    setParams({ fileRoot: 'Downloads', filePath: null });
    rerender(<FileBrowser key="Downloads" root="Downloads" />);

    expect(await screen.findByText('y.txt')).toBeInTheDocument();
    expect(mockListFiles).toHaveBeenLastCalledWith('tok-abc', 'Downloads', '');
  });
});
