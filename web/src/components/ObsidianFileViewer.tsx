import { useEffect, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { getAccessToken } from '../auth';
import { getObsidianFile } from '../api';
import { ObsidianFileEditor } from './ObsidianFileEditor';
import { setParams } from '../urlState';

export function ObsidianFileViewer({
  folder,
  file,
  initialMode = 'view',
}: {
  folder: string;
  file: string;
  initialMode?: 'view' | 'edit';
}) {
  const [content, setContent] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [mode, setMode] = useState<'view' | 'edit'>(initialMode);

  useEffect(() => {
    let active = true;
    setContent(null);
    setError(null);
    setMode(initialMode);
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getObsidianFile(token, folder, file);
        if (active) setContent(data.content);
      } catch {
        if (active) setError('File unavailable');
      }
    })();
    return () => {
      active = false;
    };
  }, [folder, file, initialMode]);

  useEffect(() => {
    setParams({ mode: mode === 'edit' ? 'edit' : null });
  }, [mode]);

  if (mode === 'edit' && content !== null) {
    return (
      <ObsidianFileEditor
        folder={folder}
        file={file}
        initialContent={content}
        onSaved={(newContent) => {
          setContent(newContent);
          setMode('view');
        }}
        onCancel={() => setMode('view')}
      />
    );
  }

  const isMarkdown = file.toLowerCase().endsWith('.md');

  return (
    <div className="rounded border bg-white p-4">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="font-medium">{file}</h3>
        {content !== null && (
          <button onClick={() => setMode('edit')} title="Edit" aria-label="Edit">
            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z" />
            </svg>
          </button>
        )}
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && content === null && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && content !== null && isMarkdown && (
        <div className="prose prose-sm max-w-none">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
        </div>
      )}
      {!error && content !== null && !isMarkdown && (
        <pre className="whitespace-pre-wrap text-sm">{content}</pre>
      )}
    </div>
  );
}
