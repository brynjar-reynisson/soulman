import { useState } from 'react';
import { getAccessToken } from '../auth';
import { createProject, updateProject, deleteProject, ApiError, type Project } from '../api';

// `projects`/`projectsError`/`refreshProjects` are owned by ProjectsPage and
// shared with PromptsPanel — see ProjectsPage.tsx for why this list isn't
// fetched locally here.
export function ProjectsPanel({
  projects,
  projectsError,
  refreshProjects,
}: {
  projects: Project[] | null;
  projectsError: string | null;
  refreshProjects: () => Promise<void>;
}) {
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [path, setPath] = useState('');
  const [editingName, setEditingName] = useState<string | null>(null);
  const [editPath, setEditPath] = useState('');

  async function handleAdd() {
    if (!name || !path) return;
    const token = await getAccessToken();
    try {
      await createProject(token, name, path);
      setName('');
      setPath('');
      setError(null);
      await refreshProjects();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to add project');
    }
  }

  async function handleDelete(projectName: string) {
    const token = await getAccessToken();
    try {
      await deleteProject(token, projectName);
      setError(null);
      await refreshProjects();
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setError(`Cannot delete "${projectName}": it still has prompts`);
      } else {
        setError('Failed to delete project');
      }
    }
  }

  function startEdit(p: Project) {
    setEditingName(p.name);
    setEditPath(p.path);
  }

  function cancelEdit() {
    setEditingName(null);
    setEditPath('');
  }

  async function handleSaveEdit(projectName: string) {
    if (!editPath) return;
    const token = await getAccessToken();
    try {
      await updateProject(token, projectName, editPath);
      setEditingName(null);
      setEditPath('');
      setError(null);
      await refreshProjects();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to update project');
    }
  }

  const displayError = error ?? projectsError;

  return (
    <div className="rounded border border-gray-200 bg-white p-4">
      <h2 className="mb-3 text-lg font-semibold">Projects</h2>
      {displayError && <p className="mb-2 text-sm text-red-600">{displayError}</p>}
      {projects === null && <p className="text-sm text-gray-500">Loading...</p>}
      {projects && (
        <table className="mb-4 w-full text-sm">
          <thead>
            <tr className="text-left text-gray-500">
              <th className="pb-1">Name</th>
              <th className="pb-1">Path</th>
              <th className="pb-1"></th>
            </tr>
          </thead>
          <tbody>
            {projects.map((p) => (
              <tr key={p.name} className="border-t border-gray-100">
                <td className="py-1 font-medium">{p.name}</td>
                <td className="py-1 text-gray-600">
                  {editingName === p.name ? (
                    <input
                      value={editPath}
                      onChange={(e) => setEditPath(e.target.value)}
                      className="w-full rounded border border-gray-300 px-2 py-1 text-sm"
                    />
                  ) : (
                    p.path
                  )}
                </td>
                <td className="py-1 text-right whitespace-nowrap">
                  {editingName === p.name ? (
                    <>
                      <button
                        onClick={() => handleSaveEdit(p.name)}
                        className="text-xs text-gray-500 underline"
                      >
                        Save
                      </button>{' '}
                      <button onClick={cancelEdit} className="text-xs text-gray-500 underline">
                        Cancel
                      </button>
                    </>
                  ) : (
                    <>
                      <button onClick={() => startEdit(p)} className="text-xs text-gray-500 underline">
                        Edit
                      </button>{' '}
                      <button onClick={() => handleDelete(p.name)} className="text-xs text-red-600 underline">
                        Delete
                      </button>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="flex gap-2">
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Project name"
          className="flex-1 rounded border border-gray-300 px-2 py-1 text-sm"
        />
        <input
          value={path}
          onChange={(e) => setPath(e.target.value)}
          placeholder="Path"
          className="flex-1 rounded border border-gray-300 px-2 py-1 text-sm"
        />
        <button onClick={handleAdd} className="rounded bg-gray-800 px-3 py-1 text-sm text-white">
          Add
        </button>
      </div>
    </div>
  );
}
