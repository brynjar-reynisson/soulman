import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getProjects, type Project } from '../api';
import { ProjectsPanel } from './ProjectsPanel';
import { PromptsPanel } from './PromptsPanel';

// The projects list is owned here, not by either panel, and passed down as
// a shared prop. ProjectsPanel and PromptsPanel each display it (the table,
// the "Select a project" dropdown) — if each fetched its own copy, adding a
// project in ProjectsPanel would never update PromptsPanel's dropdown until
// its own, separate Refresh button was clicked. One shared source of truth
// means both stay in sync automatically after any add/edit/delete.
export function ProjectsPage({ onBack }: { onBack: () => void }) {
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [projectsError, setProjectsError] = useState<string | null>(null);

  async function refreshProjects() {
    const token = await getAccessToken();
    try {
      const data = await getProjects(token);
      // Defense in depth: normalize a null/undefined API response to an
      // empty array — see NOTES.md's empty-list incident for why.
      setProjects(data ?? []);
      setProjectsError(null);
    } catch {
      setProjectsError('Projects unavailable');
    }
  }

  useEffect(() => {
    refreshProjects();
  }, []);

  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Projects</h1>
        <button onClick={onBack} className="text-sm text-gray-500 underline">
          ← Soulman
        </button>
      </div>
      <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
        <ProjectsPanel projects={projects} projectsError={projectsError} refreshProjects={refreshProjects} />
        <PromptsPanel projects={projects} refreshProjects={refreshProjects} />
      </div>
    </div>
  );
}
