import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import {
  getPrompts,
  createPrompt,
  updatePromptState,
  ApiError,
  type Prompt,
  type Project,
} from '../api';

const STATES: Prompt['state'][] = ['NOT_STARTED', 'CREATING_SPEC', 'IMPLEMENTING', 'DONE'];

// `projects`/`refreshProjects` are owned by ProjectsPage and shared with
// ProjectsPanel — see ProjectsPage.tsx for why this list isn't fetched
// locally here. `prompts` remains this component's own concern.
export function PromptsPanel({
  projects,
  refreshProjects,
}: {
  projects: Project[] | null;
  refreshProjects: () => Promise<void>;
}) {
  const [prompts, setPrompts] = useState<Prompt[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [projectName, setProjectName] = useState('');
  const [taskName, setTaskName] = useState('');
  const [promptText, setPromptText] = useState('');

  async function refreshPrompts() {
    const token = await getAccessToken();
    try {
      const data = await getPrompts(token);
      // Defense in depth: normalize a null/undefined API response to an
      // empty array — see NOTES.md's empty-list incident for why.
      setPrompts(data ?? []);
      setError(null);
    } catch {
      setError('Prompts unavailable');
    }
  }

  useEffect(() => {
    refreshPrompts();
  }, []);

  async function handleRefreshClick() {
    await Promise.all([refreshPrompts(), refreshProjects()]);
  }

  async function handleAdd() {
    if (!projectName || !taskName || !promptText) return;
    const token = await getAccessToken();
    try {
      await createPrompt(token, projectName, taskName, promptText);
      setTaskName('');
      setPromptText('');
      await refreshPrompts();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to add prompt');
    }
  }

  async function handleStateChange(id: number, state: Prompt['state']) {
    const token = await getAccessToken();
    try {
      await updatePromptState(token, id, state);
      await refreshPrompts();
    } catch {
      setError('Failed to update state');
    }
  }

  return (
    <div className="rounded border border-gray-200 bg-white p-4">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-lg font-semibold">Prompts</h2>
        <button onClick={handleRefreshClick} className="text-xs text-gray-500 underline">
          Refresh
        </button>
      </div>
      {error && <p className="mb-2 text-sm text-red-600">{error}</p>}
      {prompts === null && <p className="text-sm text-gray-500">Loading...</p>}
      {prompts && (
        // DONE prompts are hidden by default — the backend still returns
        // them (available via the API for diagnostics), this is a
        // display-only filter. A future read-only "show done" view can
        // reuse the same `prompts` state without any backend change.
        <table className="mb-4 w-full text-sm">
          <thead>
            <tr className="text-left text-gray-500">
              <th className="pb-1">Project</th>
              <th className="pb-1">Task</th>
              <th className="pb-1">State</th>
            </tr>
          </thead>
          <tbody>
            {prompts
              .filter((p) => p.state !== 'DONE')
              .map((p) => (
                <tr key={p.id} className="border-t border-gray-100">
                  <td className="py-1">{p.project_name}</td>
                  <td className="py-1">{p.task_name}</td>
                  <td className="py-1">
                    <select
                      value={p.state}
                      onChange={(e) => handleStateChange(p.id, e.target.value as Prompt['state'])}
                      className="rounded border border-gray-300 px-1 py-0.5 text-xs"
                    >
                      {STATES.map((s) => (
                        <option key={s} value={s}>
                          {s}
                        </option>
                      ))}
                    </select>
                    {p.last_launch_error && (
                      <p className="mt-1 text-xs text-red-600">{p.last_launch_error}</p>
                    )}
                  </td>
                </tr>
              ))}
          </tbody>
        </table>
      )}
      <div className="flex flex-col gap-2">
        <select
          value={projectName}
          onChange={(e) => setProjectName(e.target.value)}
          className="rounded border border-gray-300 px-2 py-1 text-sm"
        >
          <option value="">Select a project</option>
          {(projects ?? []).map((p) => (
            <option key={p.name} value={p.name}>
              {p.name}
            </option>
          ))}
        </select>
        <input
          value={taskName}
          onChange={(e) => setTaskName(e.target.value)}
          placeholder="Task name"
          className="rounded border border-gray-300 px-2 py-1 text-sm"
        />
        <textarea
          value={promptText}
          onChange={(e) => setPromptText(e.target.value)}
          placeholder="Prompt text"
          rows={3}
          className="rounded border border-gray-300 px-2 py-1 text-sm"
        />
        <button onClick={handleAdd} className="self-start rounded bg-gray-800 px-3 py-1 text-sm text-white">
          Add Prompt
        </button>
      </div>
    </div>
  );
}
