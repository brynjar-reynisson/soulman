// web/src/components/SearchPage.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ApiError } from '../api';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockSearch = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, search: (...args: unknown[]) => mockSearch(...args) };
});

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, '', '/');
});

describe('SearchPage', () => {
  it('shows the centered empty state before any search', async () => {
    const { SearchPage } = await import('./SearchPage');
    render(<SearchPage onBack={vi.fn()} />);

    expect(await screen.findByText('Soulman Search')).toBeInTheDocument();
    expect(screen.queryByText('No results found.')).not.toBeInTheDocument();
  });

  it('submits a query and renders results as links with snippets', async () => {
    mockSearch.mockResolvedValue({
      results: [{ title: 'Soulman', url: 'https://example.com/soulman', snippet: 'A personal AI agent.' }],
    });
    const { SearchPage } = await import('./SearchPage');
    render(<SearchPage onBack={vi.fn()} />);

    await userEvent.type(screen.getByLabelText('Search query'), 'soulman ai agent');
    await userEvent.click(screen.getByRole('button', { name: 'Search' }));

    expect(mockSearch).toHaveBeenCalledWith('tok-abc', 'soulman ai agent');
    const link = await screen.findByRole('link', { name: 'Soulman' });
    expect(link).toHaveAttribute('href', 'https://example.com/soulman');
    expect(link).toHaveAttribute('target', '_blank');
    expect(screen.getByText('A personal AI agent.')).toBeInTheDocument();
  });

  it('shows "No results found." for a successful search with zero results', async () => {
    mockSearch.mockResolvedValue({ results: [] });
    const { SearchPage } = await import('./SearchPage');
    render(<SearchPage onBack={vi.fn()} />);

    await userEvent.type(screen.getByLabelText('Search query'), 'nothing here');
    await userEvent.click(screen.getByRole('button', { name: 'Search' }));

    expect(await screen.findByText('No results found.')).toBeInTheDocument();
  });

  it('shows a "not configured" banner on a 503', async () => {
    mockSearch.mockRejectedValue(new ApiError(503, '/api/search?q=x failed (503)'));
    const { SearchPage } = await import('./SearchPage');
    render(<SearchPage onBack={vi.fn()} />);

    await userEvent.type(screen.getByLabelText('Search query'), 'soulman');
    await userEvent.click(screen.getByRole('button', { name: 'Search' }));

    expect(await screen.findByText('Web search is not configured')).toBeInTheDocument();
  });

  it('shows a generic failure banner on any other error', async () => {
    mockSearch.mockRejectedValue(new ApiError(502, '/api/search?q=x failed (502)'));
    const { SearchPage } = await import('./SearchPage');
    render(<SearchPage onBack={vi.fn()} />);

    await userEvent.type(screen.getByLabelText('Search query'), 'soulman');
    await userEvent.click(screen.getByRole('button', { name: 'Search' }));

    expect(await screen.findByText('Web search failed')).toBeInTheDocument();
  });

  it('does not call search when the query is empty', async () => {
    const { SearchPage } = await import('./SearchPage');
    render(<SearchPage onBack={vi.fn()} />);

    await userEvent.click(screen.getByRole('button', { name: 'Search' }));

    expect(mockSearch).not.toHaveBeenCalled();
  });

  it('restores and re-runs a search from a q URL param on mount', async () => {
    window.history.replaceState(null, '', '/?page=search&q=restored');
    mockSearch.mockResolvedValue({ results: [{ title: 'Restored', url: 'https://example.com', snippet: 's' }] });
    const { SearchPage } = await import('./SearchPage');
    render(<SearchPage onBack={vi.fn()} />);

    expect(await screen.findByRole('link', { name: 'Restored' })).toBeInTheDocument();
    expect(mockSearch).toHaveBeenCalledWith('tok-abc', 'restored');
  });
});
