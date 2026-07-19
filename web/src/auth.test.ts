import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';

const mockGetSession = vi.fn();
const mockOnAuthStateChange = vi.fn();
const mockSignInWithOAuth = vi.fn();
const mockSignOut = vi.fn();

vi.mock('@supabase/supabase-js', () => ({
  createClient: () => ({
    auth: {
      getSession: mockGetSession,
      onAuthStateChange: mockOnAuthStateChange,
      signInWithOAuth: mockSignInWithOAuth,
      signOut: mockSignOut,
    },
  }),
}));

beforeEach(() => {
  vi.clearAllMocks();
  mockOnAuthStateChange.mockReturnValue({ data: { subscription: { unsubscribe: vi.fn() } } });
});

describe('useAuth', () => {
  it('starts loading, then resolves to the session user', async () => {
    mockGetSession.mockResolvedValue({
      data: { session: { user: { id: 'u1', email: 'breynisson@gmail.com' } } },
    });
    const { useAuth } = await import('./auth');
    const { result } = renderHook(() => useAuth());

    expect(result.current.loading).toBe(true);
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.user?.email).toBe('breynisson@gmail.com');
  });

  it('resolves to no user when there is no session', async () => {
    mockGetSession.mockResolvedValue({ data: { session: null } });
    const { useAuth } = await import('./auth');
    const { result } = renderHook(() => useAuth());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.user).toBeNull();
  });

  it('signIn calls signInWithOAuth with the google provider', async () => {
    mockGetSession.mockResolvedValue({ data: { session: null } });
    const { useAuth } = await import('./auth');
    const { result } = renderHook(() => useAuth());
    await waitFor(() => expect(result.current.loading).toBe(false));

    await result.current.signIn();

    expect(mockSignInWithOAuth).toHaveBeenCalledWith(
      expect.objectContaining({ provider: 'google' }),
    );
  });
});

describe('useAuth cleanup', () => {
  it('unsubscribes from auth state changes on unmount', async () => {
    const unsubscribe = vi.fn();
    mockOnAuthStateChange.mockReturnValue({ data: { subscription: { unsubscribe } } });
    mockGetSession.mockResolvedValue({ data: { session: null } });
    const { useAuth } = await import('./auth');
    const { result, unmount } = renderHook(() => useAuth());
    await waitFor(() => expect(result.current.loading).toBe(false));

    unmount();

    expect(unsubscribe).toHaveBeenCalledTimes(1);
  });
});

describe('getAccessToken', () => {
  it('returns the session access token when present', async () => {
    mockGetSession.mockResolvedValue({ data: { session: { access_token: 'tok-123' } } });
    const { getAccessToken } = await import('./auth');
    await expect(getAccessToken()).resolves.toBe('tok-123');
  });

  it('returns null when there is no session', async () => {
    mockGetSession.mockResolvedValue({ data: { session: null } });
    const { getAccessToken } = await import('./auth');
    await expect(getAccessToken()).resolves.toBeNull();
  });
});
