import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetSystemMonitorStatus = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getSystemMonitorStatus: (...args: unknown[]) => mockGetSystemMonitorStatus(...args) };
});

beforeEach(() => vi.clearAllMocks());

describe('SystemMonitorPanel', () => {
  it('shows resource and service checks once loaded', async () => {
    mockGetSystemMonitorStatus.mockResolvedValue([
      { type: 'disk_space', key: 'C:\\', severity: 'ok', value_percent: 42, checked_at: '2026-07-20T00:00:00Z' },
      { type: 'service_health', key: 'agent-suite-backend', severity: 'ok', checked_at: '2026-07-20T00:00:00Z' },
    ]);
    const { SystemMonitorPanel } = await import('./SystemMonitorPanel');
    render(<SystemMonitorPanel />);

    expect(await screen.findByText('Disk C:\\')).toBeInTheDocument();
    expect(await screen.findByText('42%')).toBeInTheDocument();
    expect(await screen.findByText('agent-suite-backend')).toBeInTheDocument();
    expect(await screen.findByText('up')).toBeInTheDocument();
  });

  it('shows service detail when down', async () => {
    mockGetSystemMonitorStatus.mockResolvedValue([
      {
        type: 'service_health',
        key: 'agent-suite-backend',
        severity: 'critical',
        detail: 'connection refused',
        checked_at: '2026-07-20T00:00:00Z',
      },
    ]);
    const { SystemMonitorPanel } = await import('./SystemMonitorPanel');
    render(<SystemMonitorPanel />);

    expect(await screen.findByText('down')).toBeInTheDocument();
    expect(await screen.findByText('connection refused')).toBeInTheDocument();
  });

  it('shows an error banner without throwing when the fetch fails', async () => {
    mockGetSystemMonitorStatus.mockRejectedValue(new Error('network error'));
    const { SystemMonitorPanel } = await import('./SystemMonitorPanel');
    render(<SystemMonitorPanel />);

    expect(await screen.findByText(/system monitor status unavailable/i)).toBeInTheDocument();
  });

  it('shows empty-state messages when there are no checks', async () => {
    mockGetSystemMonitorStatus.mockResolvedValue([]);
    const { SystemMonitorPanel } = await import('./SystemMonitorPanel');
    render(<SystemMonitorPanel />);

    expect(await screen.findByText(/no resource checks/i)).toBeInTheDocument();
    expect(await screen.findByText(/no service checks/i)).toBeInTheDocument();
  });
});
