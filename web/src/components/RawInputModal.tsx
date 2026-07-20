import { useEffect } from 'react';
import type { RawInput } from '../api';

export function RawInputModal({ input, onClose }: { input: RawInput; onClose: () => void }) {
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onClose}>
      <div
        className="max-h-[80vh] w-full max-w-lg overflow-auto rounded bg-white p-4 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-2 flex items-center justify-between">
          <h3 className="font-medium">Raw Input</h3>
          <button onClick={onClose} className="text-sm text-gray-500 underline" aria-label="Close">
            Close
          </button>
        </div>
        <dl className="mb-3 space-y-1 text-sm">
          <div className="flex justify-between">
            <dt className="text-gray-500">Stimulus ID</dt>
            <dd>{input.stimulus_id}</dd>
          </div>
          <div className="flex justify-between">
            <dt className="text-gray-500">Channel</dt>
            <dd>{input.channel}</dd>
          </div>
          <div className="flex justify-between">
            <dt className="text-gray-500">Received At</dt>
            <dd>{input.received_at}</dd>
          </div>
          {input.override_cmd && (
            <div className="flex justify-between">
              <dt className="text-gray-500">Override Command</dt>
              <dd>{input.override_cmd}</dd>
            </div>
          )}
        </dl>
        <pre className="overflow-auto rounded bg-gray-50 p-2 text-xs">
          {JSON.stringify(input.raw_payload, null, 2)}
        </pre>
      </div>
    </div>
  );
}
