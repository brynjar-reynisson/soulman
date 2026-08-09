import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getClaudeRoots, type ClaudeRootListing } from '../api';
import { ClaudeLaunchForm } from './ClaudeLaunchForm';
import { getParam, setParams } from '../urlState';

export function ClaudeRootList() {
  const [roots, setRoots] = useState<ClaudeRootListing[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<string | null>(() => getParam('claudeRoot'));
  const [selectedFolder, setSelectedFolder] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getClaudeRoots(token);
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
                <>
                  <button
                    onClick={() => {
                      const next = expanded === root.label ? null : root.label;
                      setExpanded(next);
                      setSelectedFolder(null);
                      setParams({ claudeRoot: next });
                    }}
                    className="text-sm font-medium underline"
                  >
                    {root.label}
                  </button>
                  {expanded === root.label && (
                    <ul className="ml-4 mt-1 space-y-1">
                      {(root.folders ?? []).map((folder) => (
                        <li key={folder}>
                          <button
                            onClick={() => setSelectedFolder(selectedFolder === folder ? null : folder)}
                            className="text-sm underline"
                          >
                            {folder}
                          </button>
                          {selectedFolder === folder && <ClaudeLaunchForm root={root.label} folder={folder} />}
                        </li>
                      ))}
                    </ul>
                  )}
                </>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
