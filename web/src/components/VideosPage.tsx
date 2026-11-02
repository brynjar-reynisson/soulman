import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { listVideos, ApiError, type VideoListing } from '../api';
import { getParam, setParams } from '../urlState';
import { Accordion } from './Accordion';

// Browses the configured video folder as nested accordions: each folder is an
// accordion item that lazily loads its own listing when opened. Choosing a
// video hands (folder path, file name) to onSelect.
function VideoFolder({
  path,
  openSegments,
  onOpenChange,
  onSelect,
}: {
  path: string;
  openSegments: string[];
  onOpenChange: (segments: string[]) => void;
  onSelect: (path: string, file: string) => void;
}) {
  const [listing, setListing] = useState<VideoListing | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      try {
        const data = await listVideos(await getAccessToken(), path);
        if (active) setListing(data);
      } catch (err) {
        if (active) {
          setError(err instanceof ApiError && err.status === 404 ? 'Folder not found' : 'Unable to load folder');
        }
      }
    })();
    return () => {
      active = false;
    };
  }, [path]);

  if (error) return <p className="text-sm text-red-600">{error}</p>;
  if (!listing) return <p className="text-sm text-gray-500">Loading...</p>;

  const [openFolder, ...rest] = openSegments;
  const empty = listing.folders.length === 0 && listing.files.length === 0;

  return (
    <div className="space-y-2">
      {listing.folders.length > 0 && (
        <Accordion
          items={listing.folders.map((folder) => ({
            id: folder,
            label: folder,
            content: (
              <VideoFolder
                path={path ? `${path}/${folder}` : folder}
                openSegments={rest}
                onOpenChange={(segs) => onOpenChange([folder, ...segs])}
                onSelect={onSelect}
              />
            ),
          }))}
          openId={openFolder ?? null}
          onToggle={(id) => onOpenChange(id === null ? [] : [id])}
        />
      )}
      {listing.files.length > 0 && (
        <ul className="space-y-1">
          {listing.files.map((file) => (
            <li key={file.name} className="flex items-center gap-2">
              <button onClick={() => onSelect(path, file.name)} className="text-left text-sm underline">
                {file.name}
              </button>
              <span className="text-xs text-gray-400">{formatSize(file.size)}</span>
            </li>
          ))}
        </ul>
      )}
      {empty && <p className="text-sm text-gray-500">No videos here.</p>}
    </div>
  );
}

export function VideosPage({
  onBack,
  onSelect,
}: {
  onBack: () => void;
  onSelect: (path: string, file: string) => void;
}) {
  const [openSegments, setOpenSegments] = useState<string[]>(() => {
    const p = getParam('videoPath');
    return p ? p.split('/') : [];
  });

  function handleOpenChange(segments: string[]) {
    setOpenSegments(segments);
    setParams({ videoPath: segments.length ? segments.join('/') : null });
  }

  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Videos</h1>
        <button onClick={onBack} className="text-sm text-gray-500 underline">
          ← Soulman
        </button>
      </div>
      <VideoFolder path="" openSegments={openSegments} onOpenChange={handleOpenChange} onSelect={onSelect} />
    </div>
  );
}

function formatSize(bytes: number): string {
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}
