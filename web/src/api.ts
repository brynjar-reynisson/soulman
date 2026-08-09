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

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

async function getJSON<T>(path: string, token: string | null): Promise<T> {
  const response = await fetch(path, {
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

export interface ObsidianFolders {
  folders: string[];
}

export interface ObsidianFiles {
  files: string[];
}

export interface ObsidianFileContent {
  content: string;
}

async function mutateJSON(
  method: 'POST' | 'PUT',
  path: string,
  token: string | null,
  body: unknown,
): Promise<void> {
  const response = await fetch(path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    throw new ApiError(response.status, `${path} failed (${response.status})`);
  }
}

export const getObsidianFolders = (token: string | null): Promise<ObsidianFolders> =>
  getJSON('/api/obsidian/folders', token);

export const getObsidianFiles = (token: string | null, folder: string): Promise<ObsidianFiles> =>
  getJSON(`/api/obsidian/files?folder=${encodeURIComponent(folder)}`, token);

export const getObsidianFile = (
  token: string | null,
  folder: string,
  file: string,
): Promise<ObsidianFileContent> =>
  getJSON(`/api/obsidian/file?folder=${encodeURIComponent(folder)}&file=${encodeURIComponent(file)}`, token);

export const saveObsidianFile = (
  token: string | null,
  folder: string,
  file: string,
  content: string,
): Promise<void> => mutateJSON('PUT', '/api/obsidian/file', token, { folder, file, content });

export const createObsidianFile = (
  token: string | null,
  folder: string,
  file: string,
  content: string,
): Promise<void> => mutateJSON('POST', '/api/obsidian/file', token, { folder, file, content });

export const renameObsidianFile = (
  token: string | null,
  folder: string,
  file: string,
  newName: string,
): Promise<void> =>
  mutateJSON('POST', '/api/obsidian/file/rename', token, { folder, file, new_name: newName });

export interface ClaudeRootListing {
  label: string;
  path: string;
  exists: boolean;
  folders: string[];
}

export interface ClaudeRoots {
  roots: ClaudeRootListing[];
}

export const getClaudeRoots = (token: string | null): Promise<ClaudeRoots> =>
  getJSON('/api/claude/roots', token);

export const launchClaudeSession = (
  token: string | null,
  root: string,
  folder: string,
  sessionName: string,
): Promise<void> => mutateJSON('POST', '/api/claude/launch', token, { root, folder, sessionName });
