import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { launchClaudeSession, ApiError } from '../api';

export function ClaudeLaunchForm({ root, folder }: { root: string; folder: string }) {
  const [sessionName, setSessionName] = useState(folder);
  const [status, setStatus] = useState<'idle' | 'launching' | 'success' | 'error'>('idle');
  const [message, setMessage] = useState<string | null>(null);

  useEffect(() => {
    setSessionName(folder);
    setStatus('idle');
    setMessage(null);
  }, [folder]);

  const handleLaunch = async () => {
    setStatus('launching');
    setMessage(null);
    const token = await getAccessToken();
    try {
      await launchClaudeSession(token, root, folder, sessionName);
      setStatus('success');
      setMessage(`Session '${sessionName}' launched`);
    } catch (err) {
      setStatus('error');
      setMessage(err instanceof ApiError ? `Launch failed (${err.status})` : 'Launch failed');
    }
  };

  return (
    <div className="ml-4 mt-1 flex items-center gap-2">
      <input
        type="text"
        value={sessionName}
        onChange={(e) => setSessionName(e.target.value)}
        className="rounded border border-gray-300 px-2 py-1 text-sm"
      />
      <button
        onClick={handleLaunch}
        disabled={status === 'launching'}
        className="rounded bg-blue-600 px-3 py-1 text-sm text-white disabled:opacity-50"
      >
        Launch
      </button>
      {message && (
        <span className={`text-sm ${status === 'error' ? 'text-red-600' : 'text-green-600'}`}>{message}</span>
      )}
    </div>
  );
}
