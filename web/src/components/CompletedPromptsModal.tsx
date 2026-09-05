import { useEffect } from 'react';
import type { Prompt } from '../api';

function lastTwentyDone(prompts: Prompt[]): Prompt[] {
  return prompts
    .filter((p) => p.state === 'DONE')
    .sort((a, b) => (b.done_at ?? '').localeCompare(a.done_at ?? ''))
    .slice(0, 20);
}

export function CompletedPromptsModal({
  prompts,
  onClose,
}: {
  prompts: Prompt[];
  onClose: () => void;
}) {
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [onClose]);

  const done = lastTwentyDone(prompts);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onClose}>
      <div
        className="max-h-[80vh] w-full max-w-lg overflow-auto rounded bg-white p-4 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-2 flex items-center justify-between">
          <h3 className="font-medium">Completed Tasks</h3>
          <button
            onClick={onClose}
            className="cursor-pointer text-sm text-gray-500 underline"
            aria-label="Close"
          >
            X
          </button>
        </div>
        {done.length === 0 && <p className="text-sm text-gray-500">No completed tasks yet.</p>}
        {done.length > 0 && (
          <ul className="space-y-3 text-sm">
            {done.map((p) => (
              <li key={p.id} className="border-t border-gray-100 pt-2 first:border-t-0 first:pt-0">
                <dl className="space-y-1">
                  <div className="flex justify-between">
                    <dt className="text-gray-500">Project</dt>
                    <dd>{p.project_name}</dd>
                  </div>
                  <div className="flex justify-between">
                    <dt className="text-gray-500">Task</dt>
                    <dd>{p.task_name}</dd>
                  </div>
                  <div>
                    <dt className="text-gray-500">Prompt</dt>
                    <dd className="whitespace-pre-wrap text-xs">{p.prompt_text}</dd>
                  </div>
                </dl>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
