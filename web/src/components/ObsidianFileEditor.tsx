import { useState } from 'react';
import { getAccessToken } from '../auth';
import { saveObsidianFile } from '../api';

export function ObsidianFileEditor({
  folder,
  file,
  initialContent,
  onSaved,
  onCancel,
}: {
  folder: string;
  file: string;
  initialContent: string;
  onSaved: (content: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(initialContent);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const handleSave = async () => {
    setSaving(true);
    setError(null);
    const token = await getAccessToken();
    try {
      await saveObsidianFile(token, folder, file, value);
      onSaved(value);
    } catch {
      setError('Save failed');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="rounded border bg-white p-4">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="font-medium">{file}</h3>
        <div className="flex items-center gap-2">
          <button onClick={handleSave} disabled={saving} title="Save" aria-label="Save">
            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2Z" />
              <path d="M17 21v-8H7v8" />
              <path d="M7 3v5h8" />
            </svg>
          </button>
          <button onClick={onCancel} title="Close without saving" aria-label="Close without saving">
            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M18 6 6 18" />
              <path d="M6 6l12 12" />
            </svg>
          </button>
        </div>
      </div>
      {error && <p className="mb-2 text-sm text-red-600">{error}</p>}
      <textarea
        value={value}
        onChange={(e) => setValue(e.target.value)}
        className="h-96 w-full rounded border p-2 font-mono text-sm"
      />
    </div>
  );
}
