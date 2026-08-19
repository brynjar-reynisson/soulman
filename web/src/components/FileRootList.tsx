import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getFileBrowserRoots, type FileBrowserRootListing } from '../api';
import { FileBrowser } from './FileBrowser';
import { getParam, setParams } from '../urlState';

export function FileRootList() {
  const [roots, setRoots] = useState<FileBrowserRootListing[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedRoot, setSelectedRoot] = useState<string | null>(() => getParam('fileRoot'));

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getFileBrowserRoots(token);
        if (active) setRoots(data.roots ?? []);
      } catch {
        if (active) setError('Roots unavailable');
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  return (
    <div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && roots === null && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && roots && (
        <ul className="space-y-2">
          {roots.map((root) => (
            <li key={root.label}>
              {!root.exists && (
                <span className="text-sm font-medium text-gray-400">{root.label} (not found)</span>
              )}
              {root.exists && (
                <button
                  onClick={() => {
                    const next = selectedRoot === root.label ? null : root.label;
                    setSelectedRoot(next);
                    setParams({ fileRoot: next, filePath: null });
                  }}
                  className="text-sm font-medium underline"
                >
                  {root.label}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {selectedRoot && (
        <div className="mt-4">
          <FileBrowser key={selectedRoot} root={selectedRoot} />
        </div>
      )}
    </div>
  );
}
