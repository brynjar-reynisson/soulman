import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getSystemMonitorStatus, type ServiceStatus, type CheckStatus } from '../api';

const RESOURCE_TYPES = new Set(['disk_space', 'memory', 'cpu']);

function resourceLabel(c: CheckStatus): string {
  if (c.type === 'disk_space') return `Disk ${c.key ?? ''}`;
  if (c.type === 'memory') return 'Memory';
  if (c.type === 'cpu') return 'CPU';
  return c.type;
}

function severityColor(severity: string): string {
  if (severity === 'critical') return 'text-red-600';
  if (severity === 'warning') return 'text-amber-600';
  return 'text-green-600';
}

export function StatusPanel({ initialStatus }: { initialStatus: ServiceStatus | null }) {
  const [checks, setChecks] = useState<CheckStatus[] | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getSystemMonitorStatus(token);
        if (active) setChecks(data);
      } catch {
        // System monitor data is supplementary — Soulman's own service
        // status (initialStatus) still renders even if this fetch fails.
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  const services = initialStatus ? Object.entries(initialStatus) : [];
  const externalServices = checks?.filter((c) => c.type === 'service_health') ?? [];
  const resources = checks?.filter((c) => RESOURCE_TYPES.has(c.type)) ?? [];
  const hasAnyData = services.length > 0 || externalServices.length > 0 || resources.length > 0;

  return (
    <div className="rounded border bg-white p-4">
      <h2 className="mb-2 font-medium">System Status</h2>
      {!hasAnyData ? (
        <p className="text-sm text-gray-500">No status data</p>
      ) : (
        <ul className="space-y-1">
          {services.map(([name, state]) => (
            <li key={name} className="flex justify-between text-sm">
              <span>{name}</span>
              <span className={state === 'up' ? 'text-green-600' : 'text-red-600'}>{state}</span>
            </li>
          ))}
          {externalServices.map((c) => (
            <li key={`service:${c.key ?? ''}`} className="flex justify-between text-sm">
              <span>{c.key}</span>
              <span className={severityColor(c.severity)}>{c.severity === 'critical' ? 'down' : 'up'}</span>
            </li>
          ))}
          {resources.map((c) => (
            <li key={`resource:${c.type}:${c.key ?? ''}`} className="flex justify-between text-sm">
              <span>{resourceLabel(c)}</span>
              <span className={severityColor(c.severity)}>
                {c.value_percent !== undefined ? `${Math.round(c.value_percent)}%` : c.severity}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
