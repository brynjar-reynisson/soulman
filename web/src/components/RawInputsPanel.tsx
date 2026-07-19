import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getRawInputs, type RawInput } from '../api';

export function RawInputsPanel() {
  const [inputs, setInputs] = useState<RawInput[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await getRawInputs(token);
        if (active) setInputs(data);
      } catch {
        if (active) setError('Raw inputs unavailable');
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  return (
    <div className="rounded border bg-white p-4">
      <h2 className="mb-2 font-medium">Recent Raw Inputs</h2>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && inputs === null && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && inputs?.length === 0 && <p className="text-sm text-gray-500">No raw inputs yet</p>}
      {!error && inputs && inputs.length > 0 && (
        <ul className="space-y-2">
          {inputs.map((i) => (
            <li key={i.stimulus_id} className="text-sm">
              <span className="text-gray-400">{i.received_at}</span> [{i.channel}]{' '}
              {i.normalized_text ?? '(no text)'}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
