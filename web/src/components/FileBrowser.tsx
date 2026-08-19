// web/src/components/FileBrowser.tsx
import { useEffect, useRef, useState } from 'react';
import { getAccessToken } from '../auth';
import { listFiles, downloadFile, uploadFile, ApiError, type FileListing } from '../api';
import { getParam, setParams } from '../urlState';

export function FileBrowser({ root }: { root: string }) {
  const [currentPath, setCurrentPath] = useState<string>(() => getParam('filePath') ?? '');
  const [listing, setListing] = useState<FileListing | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pendingFile, setPendingFile] = useState<File | null>(null);
  const [conflictFile, setConflictFile] = useState<string | null>(null);
  const [uploadSuccess, setUploadSuccess] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await listFiles(token, root, currentPath);
        if (active) {
          setListing(data);
          setError(null);
        }
      } catch {
        if (active) setError('Unable to load folder');
      }
    })();
    return () => {
      active = false;
    };
  }, [root, currentPath, refreshKey]);

  function navigateTo(path: string) {
    setCurrentPath(path);
    setParams({ fileRoot: root, filePath: path || null });
  }

  const crumbs = currentPath === '' ? [] : currentPath.split('/');

  async function handleDownload(name: string) {
    const token = await getAccessToken();
    try {
      await downloadFile(token, root, currentPath, name);
    } catch {
      setError('Download failed — the file may have moved');
      setRefreshKey((k) => k + 1);
    }
  }

  async function handleUpload(file: File, overwrite: boolean) {
    const token = await getAccessToken();
    setUploadSuccess(null);
    try {
      await uploadFile(token, root, currentPath, file, overwrite);
      setConflictFile(null);
      setPendingFile(null);
      setError(null);
      setUploadSuccess(file.name);
      if (fileInputRef.current) fileInputRef.current.value = '';
      setRefreshKey((k) => k + 1);
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setConflictFile(file.name);
        setError(null);
      } else if (err instanceof ApiError && err.status === 413) {
        setError('Upload exceeds the 200MB limit');
        setConflictFile(null);
      } else {
        setError('Upload failed');
        setConflictFile(null);
      }
    }
  }

  return (
    <div>
      <nav className="mb-2 text-sm text-gray-500">
        <button onClick={() => navigateTo('')} className="underline">
          {root}
        </button>
        {crumbs.map((seg, i) => (
          <span key={i}>
            {' / '}
            <button onClick={() => navigateTo(crumbs.slice(0, i + 1).join('/'))} className="underline">
              {seg}
            </button>
          </span>
        ))}
      </nav>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!listing && !error && <p className="text-sm text-gray-500">Loading...</p>}
      {listing && (
        <ul className="space-y-1">
          {listing.folders.map((folder) => (
            <li key={folder}>
              <button
                onClick={() => navigateTo(currentPath ? `${currentPath}/${folder}` : folder)}
                className="text-sm underline"
              >
                {folder}
              </button>
            </li>
          ))}
          {listing.files.map((file) => (
            <li key={file.name} className="flex items-center gap-2">
              <span className="text-sm">{file.name}</span>
              <span className="text-xs text-gray-400">{formatSize(file.size)}</span>
              <button onClick={() => handleDownload(file.name)} className="text-sm underline">
                Download
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="mt-4">
        <input
          ref={fileInputRef}
          type="file"
          onChange={(e) => {
            setPendingFile(e.target.files?.[0] ?? null);
            setConflictFile(null);
            setUploadSuccess(null);
          }}
        />
        <button
          disabled={!pendingFile}
          onClick={() => pendingFile && handleUpload(pendingFile, false)}
          className="ml-2 text-sm underline"
        >
          Upload
        </button>
        {uploadSuccess && (
          <p className="mt-2 text-sm text-green-600">&quot;{uploadSuccess}&quot; uploaded successfully.</p>
        )}
        {conflictFile && (
          <div className="mt-2 text-sm text-red-600">
            &quot;{conflictFile}&quot; already exists —{' '}
            <button onClick={() => pendingFile && handleUpload(pendingFile, true)} className="underline">
              replace?
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}
