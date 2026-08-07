import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getObsidianFiles, createObsidianFile } from '../api';
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

  const [creating, setCreating] = useState(false);
  const [newFileName, setNewFileName] = useState('');
  const [createError, setCreateError] = useState<string | null>(null);

  const handleCreate = async () => {
    setCreateError(null);
    const token = await getAccessToken();
    try {
      await createObsidianFile(token, folder, newFileName, '');
      const data = await getObsidianFiles(token, folder);
      setFiles(data.files);
      setSelected(newFileName);
      setCreating(false);
      setNewFileName('');
    } catch {
      setCreateError('Could not create file');
    }
  };

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
      {creating ? (
        <div className="mt-2 flex items-center gap-2">
          <input
            value={newFileName}
            onChange={(e) => setNewFileName(e.target.value)}
            placeholder="filename.md"
            className="rounded border px-2 py-1 text-sm"
          />
          <button onClick={handleCreate} className="text-sm underline">
            Create
          </button>
          <button
            onClick={() => {
              setCreating(false);
              setNewFileName('');
              setCreateError(null);
            }}
            className="text-sm underline"
          >
            Cancel
          </button>
        </div>
      ) : (
        <button onClick={() => setCreating(true)} className="mt-2 text-sm underline">
          + New file
        </button>
      )}
      {createError && <p className="text-sm text-red-600">{createError}</p>}
    </div>
  );
}
