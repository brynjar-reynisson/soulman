import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getProjects, createProject, deleteProject, ApiError, type Project } from '../api';

export function ProjectsPanel() {
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [path, setPath] = useState('');

  async function refresh() {
    const token = await getAccessToken();
    try {
      const data = await getProjects(token);
      setProjects(data);
      setError(null);
    } catch {
      setError('Projects unavailable');
    }
  }

  useEffect(() => {
    refresh();
  }, []);

  async function handleAdd() {
    if (!name || !path) return;
    const token = await getAccessToken();
    try {
      await createProject(token, name, path);
      setName('');
      setPath('');
      await refresh();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to add project');
    }
  }

  async function handleDelete(projectName: string) {
    const token = await getAccessToken();
    try {
      await deleteProject(token, projectName);
      await refresh();
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setError(`Cannot delete "${projectName}": it still has prompts`);
      } else {
        setError('Failed to delete project');
      }
    }
  }

  return (
    <div className="rounded border border-gray-200 bg-white p-4">
      <h2 className="mb-3 text-lg font-semibold">Projects</h2>
      {error && <p className="mb-2 text-sm text-red-600">{error}</p>}
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
                <td className="py-1 text-gray-600">{p.path}</td>
                <td className="py-1 text-right">
                  <button onClick={() => handleDelete(p.name)} className="text-xs text-red-600 underline">
                    Delete
                  </button>
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
