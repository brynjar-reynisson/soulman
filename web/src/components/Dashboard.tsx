import type { ServiceStatus } from '../api';
import { StatusPanel } from './StatusPanel';
import { EpisodesPanel } from './EpisodesPanel';
import { RawInputsPanel } from './RawInputsPanel';
import { ReportsPanel } from './ReportsPanel';
import { SystemMonitorPanel } from './SystemMonitorPanel';

export function Dashboard({
  initialStatus,
  onSignOut,
}: {
  initialStatus: ServiceStatus | null;
  onSignOut: () => void;
}) {
  return (
    <div className="min-h-screen bg-gray-50 p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Soulman Dashboard</h1>
        <button onClick={onSignOut} className="text-sm text-gray-500 underline">
          Sign out
        </button>
      </div>
      <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
        <StatusPanel initialStatus={initialStatus} />
        <SystemMonitorPanel />
        <ReportsPanel />
        <EpisodesPanel />
        <RawInputsPanel />
      </div>
    </div>
  );
}
