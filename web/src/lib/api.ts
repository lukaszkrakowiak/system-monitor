import type {
  CommandsResponse,
  HealthResponse,
  LoginFailuresResponse,
  SelectionRequest,
  SessionsResponse,
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
