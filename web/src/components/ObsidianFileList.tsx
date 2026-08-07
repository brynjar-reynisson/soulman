import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getObsidianFiles, createObsidianFile, renameObsidianFile } from '../api';
import { ObsidianFileViewer } from './ObsidianFileViewer';

export function ObsidianFileList({ folder }: { folder: string }) {
  const [files, setFiles] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  // Tracks the filename of a file just created via handleCreate, so the
  // viewer below can be told to open it directly in edit mode instead of
  // an empty view step. Cleared as soon as the user makes any other
  // explicit selection, so it never re-applies on a later click.
  const [justCreated, setJustCreated] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setFiles(null);
    setError(null);
    setSelected(null);
    setJustCreated(null);
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getObsidianFiles(token, folder);
        if (active) setFiles(data.files ?? []);
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
      setFiles(data.files ?? []);
      setSelected(newFileName);
      setJustCreated(newFileName);
      setCreating(false);
      setNewFileName('');
    } catch {
      setCreateError('Could not create file');
    }
  };

  const [renamingFile, setRenamingFile] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [renameError, setRenameError] = useState<string | null>(null);

  const handleRename = async (oldName: string) => {
    setRenameError(null);
    const token = await getAccessToken();
    try {
      await renameObsidianFile(token, folder, oldName, renameValue);
      const data = await getObsidianFiles(token, folder);
      setFiles(data.files ?? []);
      if (selected === oldName) setSelected(renameValue);
      if (justCreated === oldName) setJustCreated(null);
      setRenamingFile(null);
    } catch {
      setRenameError('Could not rename file');
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
            <li key={f} className="flex items-center gap-2">
              {renamingFile === f ? (
                <>
                  <input
                    value={renameValue}
                    onChange={(e) => setRenameValue(e.target.value)}
                    className="rounded border px-1 py-0.5 text-sm"
                  />
                  <button onClick={() => handleRename(f)} className="text-xs underline">
                    Confirm
                  </button>
                  <button onClick={() => setRenamingFile(null)} className="text-xs underline">
                    Cancel
                  </button>
                </>
              ) : (
                <>
                  <button
                    onClick={() => {
                      if (justCreated !== f) setJustCreated(null);
                      setSelected(f);
                    }}
                    className={`text-sm underline ${selected === f ? 'font-semibold' : ''}`}
                  >
                    {f}
                  </button>
                  <button
                    onClick={() => {
                      setRenamingFile(f);
                      setRenameValue(f);
                    }}
                    title="Rename"
                    aria-label={`Rename ${f}`}
                    className="text-xs text-gray-400"
                  >
                    ✎
                  </button>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
      {selected && (
        <div className="mt-2">
          <ObsidianFileViewer
            folder={folder}
            file={selected}
            initialMode={selected === justCreated ? 'edit' : 'view'}
          />
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
      {renameError && <p className="text-sm text-red-600">{renameError}</p>}
    </div>
  );
}
