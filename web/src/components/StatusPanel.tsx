import type { ServiceStatus } from '../api';

export function StatusPanel({ initialStatus }: { initialStatus: ServiceStatus | null }) {
  const services = initialStatus ? Object.entries(initialStatus) : [];
  return (
    <div className="rounded border bg-white p-4">
      <h2 className="mb-2 font-medium">System Status</h2>
      {services.length === 0 ? (
        <p className="text-sm text-gray-500">No status data</p>
      ) : (
        <ul className="space-y-1">
          {services.map(([name, state]) => (
            <li key={name} className="flex justify-between text-sm">
              <span>{name}</span>
              <span className={state === 'up' ? 'text-green-600' : 'text-red-600'}>{state}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
