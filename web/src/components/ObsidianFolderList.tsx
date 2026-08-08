import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getObsidianFolders } from '../api';
import { ObsidianFileList } from './ObsidianFileList';
import { getParam, setParams } from '../urlState';

export function ObsidianFolderList() {
  const [folders, setFolders] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<string | null>(() => getParam('folder'));

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getObsidianFolders(token);
        if (active) setFolders(data.folders ?? []);
      } catch {
        if (active) setError('Folders unavailable');
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  return (
    <div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && folders === null && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && folders && (
        <ul className="space-y-1">
          {folders.map((f) => (
            <li key={f}>
              <button
                onClick={() => {
                  const next = expanded === f ? null : f;
                  setExpanded(next);
                  setParams({ folder: next, file: null, mode: null });
                }}
                className="text-sm font-medium underline"
              >
                {f}
              </button>
              {expanded === f && <ObsidianFileList folder={f} />}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
