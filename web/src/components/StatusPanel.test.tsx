import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockGetSystemMonitorStatus = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getSystemMonitorStatus: (...args: unknown[]) => mockGetSystemMonitorStatus(...args) };
});

beforeEach(() => {
  vi.clearAllMocks();
  mockGetSystemMonitorStatus.mockResolvedValue([]);
});

describe('StatusPanel', () => {
  it('renders each Soulman service and its up/down state', async () => {
    const { StatusPanel } = await import('./StatusPanel');
    render(<StatusPanel initialStatus={{ 'memory-svc': 'up', 'action-svc': 'down' }} />);
    expect(screen.getByText('memory-svc')).toBeInTheDocument();
    expect(screen.getByText('up')).toBeInTheDocument();
    expect(screen.getByText('action-svc')).toBeInTheDocument();
    expect(screen.getByText('down')).toBeInTheDocument();
  });

  it('shows a placeholder only when there is no status data from either source', async () => {
    const { StatusPanel } = await import('./StatusPanel');
    render(<StatusPanel initialStatus={null} />);
    expect(await screen.findByText(/no status data/i)).toBeInTheDocument();
  });

  it('shows external service checks as up/down alongside Soulman services', async () => {
    mockGetSystemMonitorStatus.mockResolvedValue([
      {
        type: 'service_health',
        key: 'agent-suite-backend',
        severity: 'critical',
        detail: 'connection refused',
        checked_at: '2026-07-20T00:00:00Z',
      },
    ]);
    const { StatusPanel } = await import('./StatusPanel');
    render(<StatusPanel initialStatus={{ 'memory-svc': 'up' }} />);

    expect(await screen.findByText('agent-suite-backend')).toBeInTheDocument();
    expect(screen.getByText('down')).toBeInTheDocument();
  });

  it('shows resource checks with their percentage value', async () => {
    mockGetSystemMonitorStatus.mockResolvedValue([
      { type: 'disk_space', key: 'C:\\', severity: 'warning', value_percent: 88, checked_at: '2026-07-20T00:00:00Z' },
    ]);
    const { StatusPanel } = await import('./StatusPanel');
    render(<StatusPanel initialStatus={{ 'memory-svc': 'up' }} />);

    expect(await screen.findByText('Disk C:\\')).toBeInTheDocument();
    expect(await screen.findByText('88%')).toBeInTheDocument();
  });

  it('still renders Soulman services if the system-monitor fetch fails', async () => {
    mockGetSystemMonitorStatus.mockRejectedValue(new Error('network error'));
    const { StatusPanel } = await import('./StatusPanel');
    render(<StatusPanel initialStatus={{ 'memory-svc': 'up' }} />);

    expect(await screen.findByText('memory-svc')).toBeInTheDocument();
  });

  it('suppresses the placeholder when external services data is provided without initialStatus', async () => {
    mockGetSystemMonitorStatus.mockResolvedValue([
      {
        type: 'service_health',
        key: 'external-api',
        severity: 'critical',
        detail: 'timeout',
        checked_at: '2026-07-20T00:00:00Z',
      },
    ]);
    const { StatusPanel } = await import('./StatusPanel');
    render(<StatusPanel initialStatus={null} />);

    expect(await screen.findByText('external-api')).toBeInTheDocument();
    expect(screen.queryByText(/no status data/i)).not.toBeInTheDocument();
  });
});
