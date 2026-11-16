import {render, screen, waitFor} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {MemoryRouter} from 'react-router-dom';
import {describe, expect, it, vi} from 'vitest';
import {App} from '../App';
import {splitList} from './settings/ConnectorsTab';
import type {AlertSettings, ConnectorInfo, ConnectorsResponse} from '../types/api';

type Handler = (url: string, init?: RequestInit) => unknown | Promise<unknown>;
function routeFetch(routes: Record<string, Handler | unknown>) {
  const fn = vi.fn(async (url: string, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${url.split('?')[0]}`;
    if (!(key in routes)) return new Response(JSON.stringify({error: 'not found'}), {status: 404});
    const r = routes[key];
    const body = typeof r === 'function' ? await (r as Handler)(url, init) : r;
    if (body instanceof Response) return body;
    return new Response(JSON.stringify(body), {status: 200, headers: {'Content-Type': 'application/json'}});
  });
  vi.stubGlobal('fetch', fn);
  return fn;
}

const renderAt = (path: string) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  );

const emailConfigured: ConnectorInfo = {
  type: 'email',
  label: 'Email (SMTP)',
  configured: true,
  enabled: true,
  has_secret: true,
  config: {host: 'smtp.example.org', port: 587, security: 'starttls', username: 'mon', from: 'Monitor <m@example.org>', to: ['a@example.org', 'b@example.org']},
};
const emailEmpty: ConnectorInfo = {type: 'email', label: 'Email (SMTP)', configured: false, enabled: false, has_secret: false, config: {}};
const connectors = (c: ConnectorInfo, secretStorage = true): ConnectorsResponse => ({connectors: [c], secret_storage: secretStorage});

const defaultSettings: AlertSettings = {
  timezone: '',
  channels: [],
  login_time: {enabled: false, windows: []},
  login_ip: {enabled: false, allowed: []},
  service_state: {enabled: false, notify_recovery: true, confirm_checks: 2},
};

const bodyOf = (init?: RequestInit) => JSON.parse(String(init?.body));
const isDisabled = (el: HTMLElement) => (el as HTMLInputElement).disabled || el.getAttribute('aria-disabled') === 'true';

describe('splitList', () => {
  it('splits on newlines, commas and semicolons and drops blanks', () => {
    expect(splitList('a@x.org\n b@x.org,, c@x.org; \n')).toEqual(['a@x.org', 'b@x.org', 'c@x.org']);
    expect(splitList('')).toEqual([]);
  });
});

describe('/settings navigation', () => {
  it('is reachable from the top navigation', async () => {
    routeFetch({'GET /api/health': {overall_status: 'OK', generated_at: '2026-11-16T10:00:00Z', systemd: [], supervisor: [], clusters: {}},
      'GET /api/settings/alerts': defaultSettings, 'GET /api/settings/connectors': connectors(emailEmpty)});
    renderAt('/health');
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', {name: 'Settings'})); // nawigacja główna to zwykłe przyciski
    expect(await screen.findByRole('tab', {name: 'Alert rules'})).toBeTruthy();
    expect(screen.getByRole('tab', {name: 'Connectors'})).toBeTruthy();
    expect(screen.getByRole('tab', {name: 'History'})).toBeTruthy();
  });
});

describe('Connectors tab', () => {
  async function openConnectors(extra: Record<string, Handler | unknown>) {
    const f = routeFetch({'GET /api/settings/alerts': defaultSettings, ...extra});
    renderAt('/settings');
    const user = userEvent.setup();
    await user.click(await screen.findByRole('tab', {name: 'Connectors'}));
    return {f, user};
  }

  it('shows saved values, keeps the password when untouched and never displays it', async () => {
    const put = vi.fn((_u: string, init?: RequestInit) => ({...emailConfigured, config: bodyOf(init).config}));
    const {user} = await openConnectors({'GET /api/settings/connectors': connectors(emailConfigured), 'PUT /api/settings/connectors/email': put});
    const host = (await screen.findByLabelText('SMTP host')) as HTMLInputElement;
    expect(host.value).toBe('smtp.example.org');
    expect((screen.getByLabelText('From') as HTMLInputElement).value).toBe('Monitor <m@example.org>');
    expect((screen.getByLabelText(/Recipients/) as HTMLTextAreaElement).value).toBe('a@example.org\nb@example.org');
    expect(screen.getByText(/Password \(saved/)).toBeTruthy();

    await user.clear(host);
    await user.type(host, 'smtp2.example.org');
    await user.click(screen.getByRole('button', {name: 'Save'}));
    await waitFor(() => expect(put).toHaveBeenCalled());
    const sent = bodyOf(put.mock.calls[0][1]);
    expect(sent.password).toBeNull(); // null = zostaw hasło
    expect(sent.enabled).toBe(true);
    expect(sent.config).toEqual({host: 'smtp2.example.org', port: 587, security: 'starttls', username: 'mon', from: 'Monitor <m@example.org>', to: ['a@example.org', 'b@example.org']});
    expect(await screen.findByText('Connector saved')).toBeTruthy();
  });

  it('sends a new password only when typed, and "" when removal is requested', async () => {
    const put = vi.fn((_u: string, init?: RequestInit) => ({...emailConfigured, has_secret: bodyOf(init).password !== ''}));
    const {user} = await openConnectors({'GET /api/settings/connectors': connectors(emailConfigured), 'PUT /api/settings/connectors/email': put});
    await user.type(await screen.findByLabelText(/^Password/), 'n3w-secret');
    await user.click(screen.getByRole('button', {name: 'Save'}));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(bodyOf(put.mock.calls[0][1]).password).toBe('n3w-secret');
    expect((screen.getByLabelText(/^Password/) as HTMLInputElement).value).toBe(''); // pole czyszczone po zapisie

    await user.click(await screen.findByLabelText('Remove saved password'));
    await user.click(screen.getByRole('button', {name: 'Save'}));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2));
    expect(bodyOf(put.mock.calls[1][1]).password).toBe('');
  });

  it('shows the server validation error and does not claim success', async () => {
    const put = vi.fn((_u: string, _i?: RequestInit) => new Response(JSON.stringify({error: 'port must be between 1 and 65535'}), {status: 400}));
    const {user} = await openConnectors({'GET /api/settings/connectors': connectors(emailConfigured), 'PUT /api/settings/connectors/email': put});
    await user.click(await screen.findByRole('button', {name: 'Save'}));
    expect(await screen.findByText('Connector was not saved')).toBeTruthy();
    expect(screen.getByText('port must be between 1 and 65535')).toBeTruthy();
    expect(screen.queryByText('Connector saved')).toBeNull();
  });

  it('sends a test email and reports success or the failure reason', async () => {
    let result: unknown = {ok: true, error: ''};
    const test = vi.fn((_u: string, _i?: RequestInit) => result);
    const {user} = await openConnectors({'GET /api/settings/connectors': connectors(emailConfigured), 'POST /api/settings/connectors/email/test': test});
    await user.click(await screen.findByRole('button', {name: 'Send test email'}));
    expect(await screen.findByText('Test message sent')).toBeTruthy();
    expect(bodyOf(test.mock.calls[0][1])).toEqual({});

    result = {ok: false, error: 'auth: 535 bad credentials'};
    await user.click(screen.getByRole('button', {name: 'Send test email'}));
    expect(await screen.findByText('Test message failed')).toBeTruthy();
    expect(screen.getByText('auth: 535 bad credentials')).toBeTruthy();
  });

  it('disables the test button until the connector is saved and warns when secret storage is off', async () => {
    await openConnectors({'GET /api/settings/connectors': connectors(emailEmpty, false)});
    const test = await screen.findByRole('button', {name: 'Send test email'});
    expect(isDisabled(test)).toBe(true);
    expect(screen.getByText('Password storage is disabled')).toBeTruthy();
    expect(isDisabled(screen.getByLabelText('Password'))).toBe(true);
  });
});

describe('Alert rules tab', () => {
  it('builds and saves rules: channel, time windows, allowed IPs and service alerts', async () => {
    const put = vi.fn((_u: string, init?: RequestInit) => bodyOf(init));
    routeFetch({'GET /api/settings/alerts': defaultSettings, 'GET /api/settings/connectors': connectors(emailConfigured), 'PUT /api/settings/alerts': put});
    renderAt('/settings');
    const user = userEvent.setup();

    await user.click(await screen.findByLabelText('Email (SMTP)'));
    const [timeSwitch, ipSwitch, serviceSwitch] = screen.getAllByLabelText('Enabled');
    await user.click(timeSwitch);
    await user.click(ipSwitch);
    await user.click(serviceSwitch);

    await user.click(screen.getByRole('button', {name: 'Add: Weeknights (Mon–Fri 17:00–06:00)'}));
    await user.click(screen.getByRole('button', {name: 'Add: Weekend (all day)'}));
    expect(screen.getAllByLabelText('From (HH:MM)')).toHaveLength(2);

    await user.type(screen.getByLabelText(/Allowed addresses/), '192.0.2.10{Enter}10.0.0.0/8');
    await user.type(screen.getByLabelText(/Time zone/), 'Europe/Warsaw');
    await user.click(screen.getByLabelText('Also notify when it recovers')); // domyślnie włączone -> wyłącz

    await user.click(screen.getByRole('button', {name: 'Save rules'}));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(bodyOf(put.mock.calls[0][1])).toEqual({
      timezone: 'Europe/Warsaw',
      channels: ['email'],
      login_time: {
        enabled: true,
        windows: [
          {days: ['mon', 'tue', 'wed', 'thu', 'fri'], from: '17:00', to: '06:00'},
          {days: ['sat', 'sun'], from: '00:00', to: '00:00'},
        ],
      },
      login_ip: {enabled: true, allowed: ['192.0.2.10', '10.0.0.0/8']},
      service_state: {enabled: true, notify_recovery: false, confirm_checks: 2},
    });
    expect(await screen.findByText('Alert rules saved')).toBeTruthy();
  });

  it('lets you remove a window and edit its days', async () => {
    const initial: AlertSettings = {...defaultSettings, login_time: {enabled: true, windows: [{days: ['mon'], from: '17:00', to: '06:00'}, {days: ['sat'], from: '00:00', to: '00:00'}]}};
    const put = vi.fn((_u: string, init?: RequestInit) => bodyOf(init));
    routeFetch({'GET /api/settings/alerts': initial, 'GET /api/settings/connectors': connectors(emailConfigured), 'PUT /api/settings/alerts': put});
    renderAt('/settings');
    const user = userEvent.setup();
    await screen.findAllByLabelText('From (HH:MM)');
    await user.click(screen.getAllByRole('button', {name: 'Remove'})[1]); // usuń okno weekendowe
    await user.click(screen.getAllByLabelText('Tue')[0]); // dodaj wtorek do pierwszego okna
    await user.click(screen.getByRole('button', {name: 'Save rules'}));
    await waitFor(() => expect(put).toHaveBeenCalled());
    expect(bodyOf(put.mock.calls[0][1]).login_time.windows).toEqual([{days: ['mon', 'tue'], from: '17:00', to: '06:00'}]);
  });

  it('disables channels whose connector is not enabled and shows server validation errors', async () => {
    const put = vi.fn((_u: string, _i?: RequestInit) => new Response(JSON.stringify({error: 'login IP rule is enabled but the allowed list is empty'}), {status: 400}));
    routeFetch({'GET /api/settings/alerts': defaultSettings, 'GET /api/settings/connectors': connectors(emailEmpty), 'PUT /api/settings/alerts': put});
    renderAt('/settings');
    const user = userEvent.setup();
    const channel = await screen.findByLabelText(/Email \(SMTP\) \(not enabled/);
    expect(isDisabled(channel)).toBe(true);
    expect(screen.getByText('No connector is enabled yet')).toBeTruthy();
    await user.click(screen.getAllByLabelText('Enabled')[1]);
    await user.click(screen.getByRole('button', {name: 'Save rules'}));
    expect(await screen.findByText('Rules were not saved')).toBeTruthy();
    expect(screen.getByText('login IP rule is enabled but the allowed list is empty')).toBeTruthy();
    expect(screen.queryByText('Alert rules saved')).toBeNull();
  });
});

describe('History tab', () => {
  it('lists notifications with delivery status and failure details', async () => {
    routeFetch({
      'GET /api/settings/alerts': defaultSettings,
      'GET /api/settings/connectors': connectors(emailConfigured),
      'GET /api/alerts/history': {
        notifications: [
          {id: 3, kind: 'service_state', connector: 'email', subject: '[srv] CRIT: Redis (was OK)', body: 'b', status: 'pending', attempts: 2, last_error: 'dial tcp: i/o timeout', created_at: '2026-11-16T10:00:00Z', sent_at: null},
          {id: 2, kind: 'login', connector: 'email', subject: '[srv] Suspicious SSH login: alice from 203.0.113.9', body: 'b', status: 'sent', attempts: 1, last_error: '', created_at: '2026-11-16T09:00:00Z', sent_at: '2026-11-16T09:00:02Z'},
          {id: 1, kind: 'service_recovery', connector: 'email', subject: '[srv] RECOVERED: Redis', body: 'b', status: 'failed', attempts: 5, last_error: 'auth failed', created_at: '2026-11-16T08:00:00Z', sent_at: null},
        ],
        limit: 50,
        offset: 0,
      },
    });
    renderAt('/settings');
    const user = userEvent.setup();
    await user.click(await screen.findByRole('tab', {name: 'History'}));
    expect(await screen.findByText('[srv] CRIT: Redis (was OK)')).toBeTruthy();
    expect(screen.getByText('Suspicious login')).toBeTruthy();
    expect(screen.getByText('Service recovered')).toBeTruthy();
    expect(screen.getByText('pending')).toBeTruthy();
    expect(screen.getByText('sent')).toBeTruthy();
    expect(screen.getByText('failed')).toBeTruthy();
    expect(screen.getByText('attempt 2: dial tcp: i/o timeout')).toBeTruthy();
    expect(screen.getByText('attempt 5: auth failed')).toBeTruthy();
  });

  it('shows an empty state', async () => {
    routeFetch({'GET /api/settings/alerts': defaultSettings, 'GET /api/settings/connectors': connectors(emailEmpty),
      'GET /api/alerts/history': {notifications: [], limit: 50, offset: 0}});
    renderAt('/settings');
    const user = userEvent.setup();
    await user.click(await screen.findByRole('tab', {name: 'History'}));
    expect(await screen.findByText('No notifications yet')).toBeTruthy();
  });
});
