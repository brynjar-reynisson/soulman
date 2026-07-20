export interface ServiceStatus {
  [service: string]: 'up' | 'down';
}

export interface Episode {
  id: number;
  stream_seq: number;
  occurred_at: string;
  received_at: string;
  source: string;
  action_type: string;
  status: string;
  task_id?: string;
  summary: string;
  decision: string;
  outcome: string;
  tags: string[];
}

export interface RawInput {
  stimulus_id: string;
  received_at: string;
  channel: string;
  normalized_text?: string;
  raw_payload: unknown;
  override_cmd?: string;
}

export interface Report {
  date: string;
  content: string;
}

export interface CheckStatus {
  type: string;
  key?: string;
  severity: 'ok' | 'warning' | 'critical';
  value_percent?: number;
  detail?: string;
  checked_at: string;
}

const WEB_SVC_URL = import.meta.env.VITE_WEB_SVC_URL as string;

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

async function getJSON<T>(path: string, token: string | null): Promise<T> {
  const response = await fetch(`${WEB_SVC_URL}${path}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (!response.ok) {
    throw new ApiError(response.status, `${path} failed (${response.status})`);
  }
  return response.json();
}

export const getStatus = (token: string | null): Promise<ServiceStatus> =>
  getJSON('/api/status', token);

export const getEpisodes = (token: string | null, limit = 20): Promise<Episode[]> =>
  getJSON(`/api/episodes?limit=${limit}`, token);

export const getRawInputs = (token: string | null, limit = 20): Promise<RawInput[]> =>
  getJSON(`/api/raw-inputs/recent?limit=${limit}`, token);

export const getLatestReport = (token: string | null): Promise<Report> =>
  getJSON('/api/reports/latest', token);

export const getReportByDate = (token: string | null, date: string): Promise<Report> =>
  getJSON(`/api/reports?date=${date}`, token);

export const getSystemMonitorStatus = (token: string | null): Promise<CheckStatus[]> =>
  getJSON('/api/system-monitor', token);
