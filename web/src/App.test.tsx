import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

const mockUseAuth = vi.fn();
vi.mock('./auth', () => ({
  useAuth: () => mockUseAuth(),
  getAccessToken: vi.fn().mockResolvedValue('tok-abc'),
}));

const mockGetStatus = vi.fn();
vi.mock('./api', async () => {
  const actual = await vi.importActual<typeof import('./api')>('./api');
  return { ...actual, getStatus: (...args: unknown[]) => mockGetStatus(...(args as [string | null])) };
});

beforeEach(() => {
  vi.clearAllMocks();
});

describe('App', () => {
  it('shows the login screen when there is no user', async () => {
    mockUseAuth.mockReturnValue({ user: null, loading: false, signIn: vi.fn(), signOut: vi.fn() });
    const { default: App } = await import('./App');
    render(<App />);

    expect(await screen.findByRole('button', { name: /sign in/i })).toBeInTheDocument();
  });

  it('shows the restricted page when /api/status returns 403', async () => {
    mockUseAuth.mockReturnValue({
      user: { email: 'someone-else@example.com' },
      loading: false,
      signIn: vi.fn(),
      signOut: vi.fn(),
    });
    const { ApiError } = await import('./api');
    mockGetStatus.mockRejectedValue(new ApiError(403, 'forbidden'));
    const { default: App } = await import('./App');
    render(<App />);

    expect(await screen.findByText(/private system/i)).toBeInTheDocument();
  });

  it('shows the dashboard when /api/status succeeds', async () => {
    mockUseAuth.mockReturnValue({
      user: { email: 'breynisson@gmail.com' },
      loading: false,
      signIn: vi.fn(),
      signOut: vi.fn(),
    });
    mockGetStatus.mockResolvedValue({ 'memory-svc': 'up' });
    const { default: App } = await import('./App');
    render(<App />);

    expect(await screen.findByText(/soulman dashboard/i)).toBeInTheDocument();
  });
});
