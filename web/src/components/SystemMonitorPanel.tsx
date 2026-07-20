import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getSystemMonitorStatus, type CheckStatus } from '../api';

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

export function SystemMonitorPanel() {
  const [checks, setChecks] = useState<CheckStatus[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getSystemMonitorStatus(token);
        if (active) setChecks(data);
      } catch {
        if (active) setError('System monitor status unavailable');
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  const resources = checks?.filter((c) => RESOURCE_TYPES.has(c.type)) ?? [];
  const services = checks?.filter((c) => c.type === 'service_health') ?? [];

  return (
    <div className="rounded border bg-white p-4">
      <h2 className="mb-2 font-medium">System Monitor</h2>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && checks === null && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && checks !== null && (
        <>
          <h3 className="mb-1 mt-2 text-sm font-medium text-gray-600">Resources</h3>
          {resources.length === 0 ? (
            <p className="text-sm text-gray-500">No resource checks</p>
          ) : (
            <ul className="space-y-1">
              {resources.map((c) => (
                <li key={`${c.type}:${c.key ?? ''}`} className="flex justify-between text-sm">
                  <span>{resourceLabel(c)}</span>
                  <span className={severityColor(c.severity)}>
                    {c.value_percent !== undefined ? `${Math.round(c.value_percent)}%` : c.severity}
                  </span>
                </li>
              ))}
            </ul>
          )}
          <h3 className="mb-1 mt-3 text-sm font-medium text-gray-600">Services</h3>
          {services.length === 0 ? (
            <p className="text-sm text-gray-500">No service checks</p>
          ) : (
            <ul className="space-y-1">
              {services.map((c) => (
                <li key={`${c.type}:${c.key ?? ''}`} className="text-sm">
                  <div className="flex justify-between">
                    <span>{c.key}</span>
                    <span className={severityColor(c.severity)}>{c.severity === 'critical' ? 'down' : 'up'}</span>
                  </div>
                  {c.severity === 'critical' && c.detail && (
                    <div className="text-xs text-gray-400">{c.detail}</div>
                  )}
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </div>
  );
}
