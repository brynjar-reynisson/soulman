import { describe, it, expect, vi, beforeEach } from 'vitest';
import { getStatus, getEpisodes, getRawInputs, getLatestReport, getReportByDate, ApiError } from './api';

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
