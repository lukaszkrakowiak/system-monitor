import type {
  AlertSettings,
  CommandsResponse,
  ConnectorInfo,
  ConnectorsResponse,
  ConnectorUpdateRequest,
  HealthResponse,
  IPLabelInfo,
  IPLabelRequest,
  IPLabelsResponse,
  LoginFailuresResponse,
  NotificationsResponse,
  SelectionRequest,
  SessionsResponse,
  TestConnectorResponse,
  UnitsResponse,
} from '../types/api';

const TOKEN_KEY = 'sm_token';

export class ApiError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

export function getToken(): string | null {
  try {
    return sessionStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

export function setToken(token: string): void {
  try {
    if (token) sessionStorage.setItem(TOKEN_KEY, token);
    else sessionStorage.removeItem(TOKEN_KEY);
  } catch {
    /* storage niedostępny — token tylko na czas życia karty niemożliwy, pomijamy */
  }
}

export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  const token = getToken();
  if (token && !headers.has('Authorization')) headers.set('Authorization', `Bearer ${token}`);
  if (init.body !== undefined && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');

  let res: Response;
  try {
    res = await fetch(path, {...init, headers, credentials: 'same-origin'});
  } catch {
    throw new ApiError(0, 'Cannot reach the server');
  }
  if (res.ok && res.status === 204) return undefined as T;
  if (!res.ok) {
    let message = `Request failed (${res.status})`;
    try {
      const body = (await res.json()) as {error?: string};
      if (body.error) message = body.error;
    } catch {
      /* ciało nie jest JSON-em */
    }
    throw new ApiError(res.status, message);
  }
  return (await res.json()) as T;
}

export type Page = {user?: string; q?: string; ip?: string; limit?: number; offset?: number};

function query(p: Page): string {
  const sp = new URLSearchParams();
  if (p.user) sp.set('user', p.user);
  if (p.q) sp.set('q', p.q);
  if (p.ip) sp.set('ip', p.ip);
  if (p.limit !== undefined) sp.set('limit', String(p.limit));
  if (p.offset !== undefined) sp.set('offset', String(p.offset));
  const s = sp.toString();
  return s ? `?${s}` : '';
}

export const getHealth = () => apiFetch<HealthResponse>('/api/health');
export const getUnits = () => apiFetch<UnitsResponse>('/api/systemd/units');
export const putSelection = (units: string[]) =>
  apiFetch<SelectionRequest>('/api/systemd/units/selection', {method: 'PUT', body: JSON.stringify({units})});
export const getSessions = (p: Page) =>
  apiFetch<SessionsResponse>(`/api/audit/sessions${query({...p, q: undefined, ip: undefined})}`);
export const getCommands = (p: Page) => apiFetch<CommandsResponse>(`/api/audit/commands${query({...p, ip: undefined})}`);
export const getLoginFailures = (p: Page) =>
  apiFetch<LoginFailuresResponse>(`/api/audit/login-failures${query({...p, q: undefined})}`);

export const getIPLabels = () => apiFetch<IPLabelsResponse>('/api/ip-labels');
export const putIPLabel = (ip: string, label: string) =>
  apiFetch<IPLabelInfo>(`/api/ip-labels/${encodeURIComponent(ip)}`, {method: 'PUT', body: JSON.stringify({label} satisfies IPLabelRequest)});
export const deleteIPLabel = (ip: string) => apiFetch<void>(`/api/ip-labels/${encodeURIComponent(ip)}`, {method: 'DELETE'});

export const getAlertSettings =() => apiFetch<AlertSettings>('/api/settings/alerts');
export const putAlertSettings = (s: AlertSettings) =>
  apiFetch<AlertSettings>('/api/settings/alerts', {method: 'PUT', body: JSON.stringify(s)});
export const getConnectors = () => apiFetch<ConnectorsResponse>('/api/settings/connectors');
export const putConnector = (type: string, req: ConnectorUpdateRequest) =>
  apiFetch<ConnectorInfo>(`/api/settings/connectors/${encodeURIComponent(type)}`, {method: 'PUT', body: JSON.stringify(req)});
export const testConnector = (type: string) =>
  apiFetch<TestConnectorResponse>(`/api/settings/connectors/${encodeURIComponent(type)}/test`, {method: 'POST', body: '{}'});
export const getAlertHistory = (p: Page) => apiFetch<NotificationsResponse>(`/api/alerts/history${query({limit: p.limit, offset: p.offset})}`);
