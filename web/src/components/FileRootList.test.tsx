import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetFileBrowserRoots = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getFileBrowserRoots: (...args: unknown[]) => mockGetFileBrowserRoots(...args) };
});

vi.mock('./FileBrowser', () => ({
  FileBrowser: ({ root }: { root: string }) => <div data-testid="file-browser">{root}</div>,
}));

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, '', '/');
});

describe('FileRootList', () => {
  it('lists existing roots and renders FileBrowser when one is selected', async () => {
    mockGetFileBrowserRoots.mockResolvedValue({
      roots: [
        { label: 'Documents', path: 'C:\\Users\\Lenovo\\Documents', exists: true },
        { label: 'Downloads', path: 'C:\\Users\\Lenovo\\Downloads', exists: true },
      ],
    });
    const { FileRootList } = await import('./FileRootList');
    render(<FileRootList />);

    await userEvent.click(await screen.findByText('Documents'));

    expect(await screen.findByTestId('file-browser')).toHaveTextContent('Documents');
  });

  it('shows a missing root as unavailable and does not let it be selected', async () => {
    mockGetFileBrowserRoots.mockResolvedValue({
      roots: [{ label: 'Downloads', path: 'C:\\Users\\Lenovo\\Downloads', exists: false }],
    });
    const { FileRootList } = await import('./FileRootList');
    render(<FileRootList />);

    expect(await screen.findByText(/downloads.*not found/i)).toBeInTheDocument();
    expect(screen.queryByTestId('file-browser')).not.toBeInTheDocument();
  });

  it('shows an error banner when roots fail to load', async () => {
    mockGetFileBrowserRoots.mockRejectedValue(new Error('network error'));
    const { FileRootList } = await import('./FileRootList');
    render(<FileRootList />);

    expect(await screen.findByText(/roots unavailable/i)).toBeInTheDocument();
  });

  it('restores the previously selected root from the URL on mount', async () => {
    window.history.replaceState(null, '', '/?fileRoot=Documents');
    mockGetFileBrowserRoots.mockResolvedValue({
      roots: [{ label: 'Documents', path: 'C:\\Users\\Lenovo\\Documents', exists: true }],
    });
    const { FileRootList } = await import('./FileRootList');
    render(<FileRootList />);

    expect(await screen.findByTestId('file-browser')).toHaveTextContent('Documents');
  });
});
