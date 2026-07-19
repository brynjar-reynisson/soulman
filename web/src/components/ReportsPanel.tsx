import { useEffect, useRef, useState } from 'react';
import { getAccessToken } from '../auth';
import { getLatestReport, getReportByDate, type Report } from '../api';

export function ReportsPanel() {
  const [report, setReport] = useState<Report | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [date, setDate] = useState('');
  const requestIdRef = useRef(0);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const load = async (selectedDate: string) => {
    const requestId = ++requestIdRef.current;
    setError(null);
    setReport(null);
    const token = await getAccessToken();
    try {
      const data = selectedDate
        ? await getReportByDate(token, selectedDate)
        : await getLatestReport(token);
      if (!mountedRef.current || requestIdRef.current !== requestId) return;
      setReport(data);
    } catch {
      if (!mountedRef.current || requestIdRef.current !== requestId) return;
      setError(selectedDate ? 'No report for this date' : 'Reports unavailable');
    }
  };

  useEffect(() => {
    load('');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="rounded border bg-white p-4">
      <div className="mb-2 flex items-center justify-between">
        <h2 className="font-medium">Daily Report</h2>
        <label htmlFor="report-date" className="sr-only">
          Report date
        </label>
        <input
          id="report-date"
          type="date"
          value={date}
          onChange={(e) => {
            setDate(e.target.value);
            load(e.target.value);
          }}
          className="rounded border px-2 py-1 text-sm"
        />
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!error && !report && <p className="text-sm text-gray-500">Loading...</p>}
      {!error && report && (
        <>
          <p className="mb-1 text-xs text-gray-400">{report.date}</p>
          <pre className="whitespace-pre-wrap text-sm">{report.content}</pre>
        </>
      )}
    </div>
  );
}
