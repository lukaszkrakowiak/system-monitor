import {describe, expect, it, vi} from 'vitest';
import {ApiError, apiFetch, getCommands, getLoginFailures, getSessions, putSelection, setToken} from './api';
import {formatBytes, formatSeconds, formatTime, statusTone, summarize} from './format';
import type {HealthResponse} from '../types/api';

function mockFetch(body: unknown, status = 200) {
  const fn = vi.fn().mockImplementation(async () => new Response(JSON.stringify(body), {status, headers: {'Content-Type': 'application/json'}}));
  vi.stubGlobal('fetch', fn);
  return fn;
}

describe('apiFetch', () => {
  it('sends Accept and no auth by default', async () => {
    const f = mockFetch({ok: 1});
    await apiFetch('/api/x');
    const init = f.mock.calls[0][1] as RequestInit;
    const h = init.headers as Headers;
    expect(h.get('Accept')).toBe('application/json');
    expect(h.get('Authorization')).toBeNull();
  });

  it('adds Bearer token from sessionStorage', async () => {
    setToken('secret-token');
    const f = mockFetch({});
    await apiFetch('/api/x');
    expect((f.mock.calls[0][1].headers as Headers).get('Authorization')).toBe('Bearer secret-token');
  });

  it('throws ApiError with server message and status', async () => {
    mockFetch({error: 'unauthorized'}, 401);
    await expect(apiFetch('/api/x')).rejects.toMatchObject({name: 'ApiError', status: 401, message: 'unauthorized'});
  });

  it('falls back to a generic message for non-JSON errors and network failures', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html>', {status: 502})));
    await expect(apiFetch('/api/x')).rejects.toMatchObject({status: 502, message: 'Request failed (502)'});
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('boom')));
    const e = (await apiFetch('/api/x').catch((x: unknown) => x)) as ApiError;
    expect(e).toBeInstanceOf(ApiError);
    expect(e.status).toBe(0);
  });

  it('putSelection sends JSON body and content type', async () => {
    const f = mockFetch({units: ['a.service']});
    const r = await putSelection(['a.service']);
    const init = f.mock.calls[0][1] as RequestInit;
    expect(f.mock.calls[0][0]).toBe('/api/systemd/units/selection');
    expect(init.method).toBe('PUT');
    expect(init.body).toBe('{"units":["a.service"]}');
    expect((init.headers as Headers).get('Content-Type')).toBe('application/json');
    expect(r.units).toEqual(['a.service']);
  });

  it('builds query strings (sessions ignore q, empty filters omitted)', async () => {
    const f = mockFetch({commands: [], limit: 50, offset: 0});
    await getCommands({user: 'anna', q: 'rm -rf', limit: 50, offset: 100});
    expect(f.mock.calls[0][0]).toBe('/api/audit/commands?user=anna&q=rm+-rf&limit=50&offset=100');
    await getCommands({user: '', q: ''});
    expect(f.mock.calls[1][0]).toBe('/api/audit/commands');
    await getSessions({user: 'bob', q: 'ignored', limit: 5});
    expect(f.mock.calls[2][0]).toBe('/api/audit/sessions?user=bob&limit=5');
    await getLoginFailures({user: 'root', ip: '203.0.113.7', q: 'ignored', limit: 50, offset: 0});
    expect(f.mock.calls[3][0]).toBe('/api/audit/login-failures?user=root&ip=203.0.113.7&limit=50&offset=0');
  });
});

describe('format', () => {
  it('maps statuses to tones', () => {
    expect(statusTone('OK')).toBe('success');
    expect(statusTone('WARN')).toBe('warning');
    expect(statusTone('DEGRADED')).toBe('warning');
    expect(statusTone('CRIT')).toBe('error');
    expect(statusTone('DOWN')).toBe('error');
    expect(statusTone('???')).toBe('neutral');
  });
  it('formats bytes, seconds and time', () => {
    expect(formatBytes(null)).toBe('—');
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(18432000)).toBe('17.6 MiB');
    expect(formatSeconds(864000)).toBe('10d 0h');
    expect(formatSeconds(3700)).toBe('1h 1m');
    expect(formatSeconds(90)).toBe('1m');
    expect(formatTime(null)).toBe('—');
    expect(formatTime('garbage')).toBe('garbage');
    expect(formatTime('2026-11-15T10:00:00Z')).not.toBe('—');
  });
  it('summarizes health', () => {
    const h: HealthResponse = {
      overall_status: 'WARN', generated_at: '2026-11-15T10:00:00Z',
      systemd: [
        {unit: 'a.service', display_name: 'a', active: 'active', sub: 'running', status: 'OK', memory_bytes: null, uptime: ''},
        {unit: 'b.service', display_name: 'b', active: 'failed', sub: 'failed', status: 'CRIT', memory_bytes: null, uptime: ''},
      ],
      supervisor: [{group: 'g', running: 1, total: 2, status: 'DEGRADED', workers: []}],
      clusters: {redis: {status: 'OK', ping_ms: 1, used_memory_human: '1M', connected_clients: 1, uptime_in_seconds: 1}},
    };
    expect(summarize(h)).toEqual({systemd: {ok: 1, total: 2}, supervisor: {ok: 0, total: 1}, clusters: {ok: 1, total: 1}});
  });
});
