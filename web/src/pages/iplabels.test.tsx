import {render, screen, waitFor} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {MemoryRouter} from 'react-router-dom';
import {describe, expect, it, vi} from 'vitest';
import {App} from '../App';
import {normalizeIp} from '../components/IpCell';

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

const sessions = {
  sessions: [
    {id: 2, username: 'anna', remote_ip: '::ffff:10.0.0.1', tty: 'ssh', pam_service: 'sshd', login_at: '2026-11-15T09:00:00Z', logout_at: null},
    {id: 1, username: 'bob', remote_ip: '203.0.113.7', tty: 'ssh', pam_service: 'sshd', login_at: '2026-11-15T08:00:00Z', logout_at: null},
  ],
  limit: 50,
  offset: 0,
};
const commands = {commands: [], limit: 50, offset: 0};

async function openSessions() {
  render(
    <MemoryRouter initialEntries={['/audit']}>
      <App />
    </MemoryRouter>,
  );
  const user = userEvent.setup();
  await user.click(await screen.findByRole('tab', {name: 'SSH sessions'}));
  await screen.findByText('203.0.113.7');
  return user;
}

describe('normalizeIp', () => {
  it('matches the server-side canonical form', () => {
    expect(normalizeIp('::FFFF:10.0.0.1')).toBe('10.0.0.1');
    expect(normalizeIp(' 2001:DB8::1 ')).toBe('2001:db8::1');
    expect(normalizeIp('203.0.113.7')).toBe('203.0.113.7');
  });
});

describe('IP labels on /audit', () => {
  it('shows labels next to IPs (also for IPv4-mapped addresses)', async () => {
    routeFetch({
      'GET /api/audit/commands': commands,
      'GET /api/audit/sessions': sessions,
      'GET /api/ip-labels': {labels: [{ip: '10.0.0.1', label: 'office'}]},
    });
    await openSessions();
    expect(await screen.findByText('office')).toBeTruthy();
    expect(screen.getAllByRole('button', {name: 'Edit label'})).toHaveLength(1);
    expect(screen.getAllByRole('button', {name: 'Add label'})).toHaveLength(1); // 203.0.113.7 bez etykiety
  });

  it('adds a label with PUT and shows it', async () => {
    let labels: {ip: string; label: string}[] = [];
    const put = vi.fn((_url: string, init?: RequestInit) => {
      const {label} = JSON.parse(String(init?.body)) as {label: string};
      labels = [{ip: '203.0.113.7', label}];
      return {ip: '203.0.113.7', label};
    });
    const fn = routeFetch({
      'GET /api/audit/commands': commands,
      'GET /api/audit/sessions': sessions,
      'GET /api/ip-labels': () => ({labels}),
      'PUT /api/ip-labels/203.0.113.7': put,
    });
    const user = await openSessions();
    await user.click(screen.getAllByRole('button', {name: 'Add label'})[1]); // wiersz bob (203.0.113.7)
    await user.type(screen.getByLabelText('Label for 203.0.113.7'), '  pnl ');
    await user.click(screen.getByRole('button', {name: 'Save'}));
    expect(await screen.findByText('pnl')).toBeTruthy();
    const call = fn.mock.calls.find((c) => (c as unknown as [string, RequestInit?])[1]?.method === 'PUT') as unknown as [string, RequestInit];
    expect(JSON.parse(String(call[1].body))).toEqual({label: 'pnl'});
    expect(screen.queryByLabelText('Label for 203.0.113.7')).toBeNull(); // edytor zamknięty
  });

  it('encodes IPv6 addresses in the URL', async () => {
    const v6 = {...sessions, sessions: [{...sessions.sessions[0], remote_ip: '2001:db8::1'}]};
    const put = vi.fn(() => ({ip: '2001:db8::1', label: 'x'}));
    routeFetch({
      'GET /api/audit/commands': commands,
      'GET /api/audit/sessions': v6,
      'GET /api/ip-labels': {labels: []},
      'PUT /api/ip-labels/2001%3Adb8%3A%3A1': put,
    });
    render(
      <MemoryRouter initialEntries={['/audit']}>
        <App />
      </MemoryRouter>,
    );
    const user = userEvent.setup();
    await user.click(await screen.findByRole('tab', {name: 'SSH sessions'}));
    await user.click(await screen.findByRole('button', {name: 'Add label'}));
    await user.type(screen.getByLabelText('Label for 2001:db8::1'), 'x');
    await user.click(screen.getByRole('button', {name: 'Save'}));
    await waitFor(() => expect(put).toHaveBeenCalled());
  });

  it('removes a label with DELETE (204 without body)', async () => {
    let labels = [{ip: '203.0.113.7', label: 'pnl'}];
    const del = vi.fn(() => {
      labels = [];
      return new Response(null, {status: 204});
    });
    routeFetch({
      'GET /api/audit/commands': commands,
      'GET /api/audit/sessions': sessions,
      'GET /api/ip-labels': () => ({labels}),
      'DELETE /api/ip-labels/203.0.113.7': del,
    });
    const user = await openSessions();
    await user.click(await screen.findByRole('button', {name: 'Edit label'}));
    await user.click(screen.getByRole('button', {name: 'Remove label'}));
    await waitFor(() => expect(del).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByText('pnl')).toBeNull());
    expect(screen.getAllByRole('button', {name: 'Add label'})).toHaveLength(2);
  });

  it('validates locally and shows server errors without closing the editor', async () => {
    routeFetch({
      'GET /api/audit/commands': commands,
      'GET /api/audit/sessions': sessions,
      'GET /api/ip-labels': {labels: []},
      'PUT /api/ip-labels/203.0.113.7': new Response(JSON.stringify({error: 'too many labels'}), {status: 400}),
    });
    const user = await openSessions();
    await user.click(screen.getAllByRole('button', {name: 'Add label'})[1]);
    await user.click(screen.getByRole('button', {name: 'Save'}));
    expect(await screen.findByText('Label must not be empty')).toBeTruthy();
    await user.type(screen.getByLabelText('Label for 203.0.113.7'), 'pnl');
    await user.click(screen.getByRole('button', {name: 'Save'}));
    expect(await screen.findByText('too many labels')).toBeTruthy();
    expect(screen.getByLabelText('Label for 203.0.113.7')).toBeTruthy();
    await user.click(screen.getByRole('button', {name: 'Cancel'}));
    expect(screen.queryByLabelText('Label for 203.0.113.7')).toBeNull();
  });

  it('still lists sessions when labels cannot be loaded', async () => {
    routeFetch({'GET /api/audit/commands': commands, 'GET /api/audit/sessions': sessions}); // /api/ip-labels -> 404
    await openSessions();
    expect(screen.getByText('anna')).toBeTruthy();
    expect(screen.getAllByRole('button', {name: 'Add label'})).toHaveLength(2);
  });
});
