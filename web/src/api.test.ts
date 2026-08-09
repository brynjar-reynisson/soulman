import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  getStatus,
  getEpisodes,
  getRawInputs,
  getLatestReport,
  getReportByDate,
  getObsidianFolders,
  getObsidianFiles,
  getObsidianFile,
  saveObsidianFile,
  createObsidianFile,
  renameObsidianFile,
  getClaudeRoots,
  launchClaudeSession,
  ApiError,
} from './api';

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn());
});

describe('getStatus', () => {
  it('attaches the bearer token and returns parsed JSON on success', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ 'memory-svc': 'up', 'action-svc': 'down' }),
    });

    const result = await getStatus('tok-abc');

    expect(result).toEqual({ 'memory-svc': 'up', 'action-svc': 'down' });
    const [url, options] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/status');
    expect(options.headers.Authorization).toBe('Bearer tok-abc');
  });

  it('omits the Authorization header when token is null', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });

    await getStatus(null);

    const [, options] = mockFetch.mock.calls[0];
    expect(options.headers.Authorization).toBeUndefined();
  });

  it('throws ApiError with the response status on a non-2xx response', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: false, status: 403, json: async () => ({}) });

    await expect(getStatus('tok-abc')).rejects.toThrow(ApiError);
    await expect(getStatus('tok-abc')).rejects.toMatchObject({ status: 403 });
  });
});

describe('getEpisodes', () => {
  it('passes the limit query param', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => [] });

    await getEpisodes('tok-abc', 5);

    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/episodes');
    expect(url).toContain('limit=5');
  });
});

describe('getRawInputs', () => {
  it('calls the raw-inputs/recent endpoint', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => [] });

    await getRawInputs('tok-abc');

    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/raw-inputs/recent');
  });
});

describe('getLatestReport', () => {
  it('calls the reports/latest endpoint', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({ date: '2026-07-19', content: 'x' }) });

    const result = await getLatestReport('tok-abc');

    expect(result.content).toBe('x');
    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/reports/latest');
  });
});

describe('getReportByDate', () => {
  it('passes the date query param', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({ date: '2026-06-01', content: 'y' }) });

    await getReportByDate('tok-abc', '2026-06-01');

    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('date=2026-06-01');
  });
});

describe('getObsidianFolders', () => {
  it('calls the obsidian/folders endpoint', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({ folders: ['soulman'] }) });

    const result = await getObsidianFolders('tok-abc');

    expect(result.folders).toEqual(['soulman']);
    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/obsidian/folders');
  });
});

describe('getObsidianFiles', () => {
  it('passes the folder query param', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({ files: [] }) });

    await getObsidianFiles('tok-abc', 'soulman');

    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/obsidian/files');
    expect(url).toContain('folder=soulman');
  });
});

describe('getObsidianFile', () => {
  it('passes folder and file query params', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({ content: 'hi' }) });

    const result = await getObsidianFile('tok-abc', 'soulman', 'NOTES.md');

    expect(result.content).toBe('hi');
    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('folder=soulman');
    expect(url).toContain('file=NOTES.md');
  });
});

describe('saveObsidianFile', () => {
  it('sends a PUT with the folder/file/content body', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });

    await saveObsidianFile('tok-abc', 'soulman', 'NOTES.md', 'new content');

    const [url, options] = mockFetch.mock.calls[0];
    expect(url).toBe('/api/obsidian/file');
    expect(options.method).toBe('PUT');
    expect(JSON.parse(options.body)).toEqual({ folder: 'soulman', file: 'NOTES.md', content: 'new content' });
  });

  it('throws ApiError on a non-2xx response', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: false, status: 404, json: async () => ({}) });

    await expect(saveObsidianFile('tok-abc', 'soulman', 'missing.md', 'x')).rejects.toThrow(ApiError);
  });
});

describe('createObsidianFile', () => {
  it('sends a POST with the folder/file/content body', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });

    await createObsidianFile('tok-abc', 'soulman', 'new.md', '');

    const [url, options] = mockFetch.mock.calls[0];
    expect(url).toBe('/api/obsidian/file');
    expect(options.method).toBe('POST');
    expect(JSON.parse(options.body)).toEqual({ folder: 'soulman', file: 'new.md', content: '' });
  });
});

describe('renameObsidianFile', () => {
  it('sends a POST to the rename endpoint with new_name', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });

    await renameObsidianFile('tok-abc', 'soulman', 'old.md', 'new.md');

    const [url, options] = mockFetch.mock.calls[0];
    expect(url).toBe('/api/obsidian/file/rename');
    expect(options.method).toBe('POST');
    expect(JSON.parse(options.body)).toEqual({ folder: 'soulman', file: 'old.md', new_name: 'new.md' });
  });
});

describe('getClaudeRoots', () => {
  it('calls the claude/roots endpoint', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: async () => ({ roots: [] }) });

    const result = await getClaudeRoots('tok-abc');

    expect(result.roots).toEqual([]);
    const [url] = mockFetch.mock.calls[0];
    expect(url).toContain('/api/claude/roots');
  });
});

describe('launchClaudeSession', () => {
  it('sends a POST with root/folder/sessionName body', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: true, status: 204, json: async () => ({}) });

    await launchClaudeSession('tok-abc', 'Obsidian', 'soulman', 'soulman');

    const [url, options] = mockFetch.mock.calls[0];
    expect(url).toBe('/api/claude/launch');
    expect(options.method).toBe('POST');
    expect(JSON.parse(options.body)).toEqual({ root: 'Obsidian', folder: 'soulman', sessionName: 'soulman' });
  });

  it('throws ApiError on a non-2xx response', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: false, status: 500, json: async () => ({}) });

    await expect(launchClaudeSession('tok-abc', 'Obsidian', 'soulman', 'x')).rejects.toThrow(ApiError);
  });
});
