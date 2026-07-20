import { useEffect, useState } from 'react';
import { getAccessToken } from '../auth';
import { getRawInputs, type RawInput } from '../api';
import { RawInputModal } from './RawInputModal';

export function RawInputsPanel() {
  const [inputs, setInputs] = useState<RawInput[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<RawInput | null>(null);

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
            <li
              key={i.stimulus_id}
              className="cursor-pointer text-sm"
              role="button"
              onClick={() => setSelected(i)}
            >
              <div className="text-gray-400">
                {i.received_at} [{i.channel}]
              </div>
              <div className="line-clamp-2">{i.normalized_text ?? '(no text)'}</div>
            </li>
          ))}
        </ul>
      )}
      {selected && <RawInputModal input={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}
