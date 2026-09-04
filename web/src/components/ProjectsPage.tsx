import { ProjectsPanel } from './ProjectsPanel';
import { PromptsPanel } from './PromptsPanel';

export function ProjectsPage({ onBack }: { onBack: () => void }) {
  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Projects</h1>
        <button onClick={onBack} className="text-sm text-gray-500 underline">
          ← Soulman
        </button>
      </div>
      <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
        <ProjectsPanel />
        <PromptsPanel />
      </div>
    </div>
  );
}
