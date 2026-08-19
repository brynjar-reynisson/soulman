// web/src/components/FileBrowser.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ApiError } from '../api';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockListFiles = vi.fn();
const mockDownloadFile = vi.fn();
const mockUploadFile = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    listFiles: (...args: unknown[]) => mockListFiles(...args),
    downloadFile: (...args: unknown[]) => mockDownloadFile(...args),
    uploadFile: (...args: unknown[]) => mockUploadFile(...args),
  };
});

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, '', '/');
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

    await userEvent.click(await screen.findByText('Download'));

    expect(mockDownloadFile).toHaveBeenCalledWith('tok-abc', 'Documents', '', 'note.txt');
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
});
