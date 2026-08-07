import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getObsidianFiles } from '../api';
import { ObsidianFileViewer } from './ObsidianFileViewer';

export function ObsidianFileList({ folder }: { folder: string }) {
  const [files, setFiles] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setFiles(null);
    setError(null);
    setSelected(null);
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getObsidianFiles(token, folder);
        if (active) setFiles(data.files);
      } catch {
        if (active) setError('Files unavailable');
      }
    })();
    return () => {
      active = false;
    };
  }, [folder]);

  return (
    <div className="ml-4 mt-2">
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && files === null && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && files?.length === 0 && <p className="text-sm text-gray-500">No files</p>}
      {!error && files && files.length > 0 && (
        <ul className="space-y-1">
          {files.map((f) => (
            <li key={f}>
              <button
                onClick={() => setSelected(f)}
                className={`text-sm underline ${selected === f ? 'font-semibold' : ''}`}
              >
                {f}
              </button>
            </li>
          ))}
        </ul>
      )}
      {selected && (
        <div className="mt-2">
          <ObsidianFileViewer folder={folder} file={selected} />
        </div>
      )}
    </div>
  );
}
