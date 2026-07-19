import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getEpisodes, type Episode } from '../api';

export function EpisodesPanel() {
  const [episodes, setEpisodes] = useState<Episode[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getEpisodes(token);
        if (active) setEpisodes(data);
      } catch {
        if (active) setError('Episodes unavailable');
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  return (
    <div className="rounded border bg-white p-4">
      <h2 className="mb-2 font-medium">Recent Episodes</h2>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && episodes === null && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && episodes?.length === 0 && <p className="text-sm text-gray-500">No episodes yet</p>}
      {!error && episodes && episodes.length > 0 && (
        <ul className="space-y-2">
          {episodes.map((e) => (
            <li key={e.id} className="text-sm">
              <span className="text-gray-400">{e.occurred_at}</span> — {e.summary}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
