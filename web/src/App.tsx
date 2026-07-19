import { useEffect, useState } from 'react';
import { useAuth, getAccessToken } from './auth';
import { getStatus, ApiError, type ServiceStatus } from './api';
import { LoginScreen } from './components/LoginScreen';
import { RestrictedScreen } from './components/RestrictedScreen';
import { Dashboard } from './components/Dashboard';

type ViewState = 'loading' | 'login' | 'restricted' | 'dashboard';

function App() {
  const { user, loading: authLoading, signIn, signOut } = useAuth();
  const [view, setView] = useState<ViewState>('loading');
  const [status, setStatus] = useState<ServiceStatus | null>(null);

  useEffect(() => {
    if (authLoading) return;
    if (!user) {
      setView('login');
      return;
    }
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const s = await getStatus(token);
        if (!active) return;
        setStatus(s);
        setView('dashboard');
      } catch (err) {
        if (!active) return;
        if (err instanceof ApiError && err.status === 403) {
          setView('restricted');
        } else {
          setView('login');
        }
      }
    })();
    return () => {
      active = false;
    };
  }, [user, authLoading]);

  if (view === 'loading') return <div className="p-8 text-center">Loading...</div>;
  if (view === 'login') return <LoginScreen onSignIn={signIn} />;
  if (view === 'restricted') return <RestrictedScreen onSignOut={signOut} />;

  return <Dashboard initialStatus={status} onSignOut={signOut} />;
}

export default App;
