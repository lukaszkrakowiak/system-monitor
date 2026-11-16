import {render, screen, waitFor, within} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {MemoryRouter} from 'react-router-dom';
import {describe, expect, it, vi} from 'vitest';
import {App} from '../App';
import type {HealthResponse} from '../types/api';

const health: HealthResponse = {
  overall_status: 'WARN',
  generated_at: '2026-11-15T10:30:00Z',
  systemd: [
    {unit: 'nginx.service', display_name: 'Nginx', active: 'active', sub: 'running', status: 'OK', memory_bytes: 18432000, uptime: '12d 4h'},
    {unit: 'php8.4-fpm.service', display_name: 'PHP', active: 'failed', sub: 'failed', status: 'CRIT', memory_bytes: null, uptime: ''},
  ],
  supervisor: [
    {group: 'messenger_async', running: 4, total: 4, status: 'OK', workers: [{name: 'messenger_async_00', state: 'RUNNING', pid: 4101}]},
    {group: 'mailer', running: 1, total: 2, status: 'DEGRADED', workers: []},
  ],
  clusters: {
    elasticsearch: {status: 'OK', cluster_name: 'prod-es', cluster_status: 'green', nodes: 3, unassigned_shards: 0, active_shards_percent: 100},
    redis: {status: 'CRIT', ping_ms: 0, used_memory_human: '', connected_clients: 0, uptime_in_seconds: 0, error: 'dial tcp: connection refused'},
  },
};

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

describe('/health', () => {
  it('renders tiles, systemd table, supervisor groups and cluster cards', async () => {
    routeFetch({'GET /api/health': health});
    renderAt('/health');
    expect(await screen.findByText('Nginx')).toBeTruthy();
    expect(screen.getByText('12d 4h')).toBeTruthy();
    expect(screen.getByText('17.6 MiB')).toBeTruthy();
    expect(screen.getByText('messenger_async')).toBeTruthy();
    expect(screen.getByText('DEGRADED')).toBeTruthy();
    expect(screen.getByText('4 / 4 running')).toBeTruthy();
    expect(screen.getByText('prod-es')).toBeTruthy();
    expect(screen.getByText('dial tcp: connection refused')).toBeTruthy(); // błąd sondy Redis
    expect(screen.queryByText('Kibana')).toBeNull(); // niekonfigurowana sonda pominięta
    expect(screen.getAllByText('1 / 2')).toHaveLength(3); // kafelki: systemd, supervisor, clusters
  });

  it('shows an empty state with a link when no services are selected', async () => {
    routeFetch({'GET /api/health': {...health, systemd: [], supervisor: [], clusters: {}}});
    renderAt('/health');
    expect(await screen.findByText('No services selected')).toBeTruthy();
    expect(screen.getByText('No cluster probes configured.')).toBeTruthy();
  });

  it('asks for a token on 401 and stores it', async () => {
    const reload = vi.fn();
    vi.stubGlobal('location', {...window.location, reload});
    routeFetch({'GET /api/health': new Response(JSON.stringify({error: 'unauthorized'}), {status: 401})});
    renderAt('/health');
    expect(await screen.findByText('Authentication required')).toBeTruthy();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('API token'), 'my-secret-token-123');
    await user.click(screen.getByRole('button', {name: 'Sign in'}));
    expect(sessionStorage.getItem('sm_token')).toBe('my-secret-token-123');
    expect(reload).toHaveBeenCalled();
  });

  it('shows a generic error banner for other failures', async () => {
    routeFetch({'GET /api/health': new Response(JSON.stringify({error: 'internal error'}), {status: 500})});
    renderAt('/health');
    expect(await screen.findByText('internal error')).toBeTruthy();
  });
});

describe('/services', () => {
  const units = {
    units: [
      {unit: 'nginx.service', description: 'web server', load: 'loaded', active: 'active', sub: 'running', selected: true},
      {unit: 'redis.service', description: 'key-value store', load: 'loaded', active: 'inactive', sub: 'dead', selected: false},
      {unit: 'cron.service', description: 'scheduler', load: 'loaded', active: 'active', sub: 'running', selected: false},
    ],
  };

  it('filters by search and saves the whole selection when toggled', async () => {
    let current = ['nginx.service']; // stan "serwera"
    const put = vi.fn((_u: string, init?: RequestInit) => {
      current = JSON.parse(init!.body as string).units;
      return {units: current};
    });
    const f = routeFetch({
      'GET /api/systemd/units': () => ({units: units.units.map((u) => ({...u, selected: current.includes(u.unit)}))}),
      'PUT /api/systemd/units/selection': put,
    });
    renderAt('/services');
    expect(await screen.findByText('nginx.service')).toBeTruthy();
    const user = userEvent.setup();

    await user.type(screen.getByLabelText('Search services'), 'redis');
    await waitFor(() => expect(screen.queryByText('nginx.service')).toBeNull());
    expect(screen.getByText('redis.service')).toBeTruthy();

    await user.click(screen.getByLabelText('Monitor redis.service'));
    await waitFor(() => expect(put).toHaveBeenCalled());
    expect(JSON.parse(put.mock.calls[0][1]!.body as string)).toEqual({units: ['nginx.service', 'redis.service']});
    expect((put.mock.calls[0][1]!.headers as Headers).get('Content-Type')).toBe('application/json');
    await waitFor(() => expect((screen.getByLabelText('Monitor redis.service') as HTMLInputElement).checked).toBe(true));
    expect(f).toHaveBeenCalled();
  });

  it('rolls back the toggle and reports an error when saving fails', async () => {
    routeFetch({
      'GET /api/systemd/units': units,
      'PUT /api/systemd/units/selection': new Response(JSON.stringify({error: 'invalid unit name'}), {status: 400}),
    });
    renderAt('/services');
    await screen.findByText('cron.service');
    const user = userEvent.setup();
    await user.click(screen.getByLabelText('Monitor cron.service'));
    expect(await screen.findByText('invalid unit name')).toBeTruthy();
    await waitFor(() => expect((screen.getByLabelText('Monitor cron.service') as HTMLInputElement).checked).toBe(false));
  });
});

describe('/audit', () => {
  const commands = {
    commands: [
      {id: 2, auid: 1000, login_username: 'anna', effective_username: 'root', cwd: '/root', command: 'systemctl restart nginx', exit_code: 0, executed_at: '2026-11-15T09:01:00Z'},
      {id: 1, auid: 1001, login_username: 'bob', effective_username: 'bob', cwd: '/home/bob', command: 'ls -la', exit_code: null, executed_at: '2026-11-15T09:00:00Z'},
    ],
    limit: 50,
    offset: 0,
  };
  const sessions = {
    sessions: [
      {id: 1, username: 'anna', remote_ip: '10.0.0.1', tty: 'ssh', pam_service: 'sshd', login_at: '2026-11-15T09:00:00Z', logout_at: null},
    ],
    limit: 50,
    offset: 0,
  };

  it('lists commands, applies user/phrase filters and switches to SSH sessions', async () => {
    const cmd = vi.fn(() => commands);
    routeFetch({'GET /api/audit/commands': cmd, 'GET /api/audit/sessions': sessions});
    renderAt('/audit');
    expect(await screen.findByText('systemctl restart nginx')).toBeTruthy();
    expect(screen.getByText('root')).toBeTruthy();
    expect(screen.getByText('/root')).toBeTruthy();

    const user = userEvent.setup();
    await user.type(screen.getByLabelText('User'), 'anna');
    await user.type(screen.getByLabelText('Command contains'), 'nginx');
    await user.click(screen.getByRole('button', {name: 'Filter'}));
    await waitFor(() => expect(cmd.mock.calls.some((c) => (c as unknown as string[])[0].includes('user=anna&q=nginx'))).toBe(true));

    await user.click(screen.getByRole('tab', {name: 'SSH sessions'}));
    expect(await screen.findByText('10.0.0.1')).toBeTruthy();
    expect(screen.getByText('active')).toBeTruthy(); // brak logout_at
  });

  it('shows failed logins and filters them by IP', async () => {
    const failures = {
      failures: [
        {id: 2, username: 'admin', remote_ip: '203.0.113.7', service: 'sshd', attempted_at: '2026-11-16T07:36:57Z'},
        {id: 1, username: 'alice', remote_ip: '', service: 'sshd', attempted_at: '2026-11-16T07:36:54Z'},
      ],
      limit: 50,
      offset: 0,
    };
    const fl = vi.fn(() => failures);
    routeFetch({'GET /api/audit/commands': commands, 'GET /api/audit/login-failures': fl});
    renderAt('/audit');
    await screen.findByText('systemctl restart nginx');
    const user = userEvent.setup();
    await user.click(screen.getByRole('tab', {name: 'Failed logins'}));
    expect(await screen.findByText('203.0.113.7')).toBeTruthy();
    expect(screen.getByText('admin')).toBeTruthy();
    expect(screen.getByText('—')).toBeTruthy(); // brak IP

    await user.type(screen.getByLabelText('Remote IP'), '203.0.113.7');
    await user.click(screen.getByRole('button', {name: 'Filter'}));
    await waitFor(() => expect(fl.mock.calls.some((c) => (c as unknown as string[])[0].includes('ip=203.0.113.7'))).toBe(true));
  });

  it('shows an empty state when there are no failed logins', async () => {
    routeFetch({'GET /api/audit/commands': commands, 'GET /api/audit/login-failures': {failures: [], limit: 50, offset: 0}});
    renderAt('/audit');
    const user = userEvent.setup();
    await user.click(await screen.findByRole('tab', {name: 'Failed logins'}));
    expect(await screen.findByText('No failed logins found')).toBeTruthy();
  });

  it('paginates with Next / Previous using offset', async () => {
    const many = {
      commands: Array.from({length: 50}, (_, i) => ({
        id: i + 1, auid: 1000, login_username: 'anna', effective_username: 'root', cwd: '/', command: `cmd-${i}`, exit_code: 0,
        executed_at: '2026-11-15T09:00:00Z',
      })),
      limit: 50,
      offset: 0,
    };
    const cmd = vi.fn((url: string) => ({...many, offset: Number(new URL(url, 'http://x').searchParams.get('offset') ?? 0)}));
    routeFetch({'GET /api/audit/commands': cmd});
    renderAt('/audit');
    await screen.findByText('cmd-0');
    const user = userEvent.setup();
    const prev = screen.getByRole('button', {name: 'Previous'});
    expect(prev.hasAttribute('disabled') || prev.getAttribute('aria-disabled') === 'true').toBe(true);
    await user.click(screen.getByRole('button', {name: 'Next'}));
    await waitFor(() => expect(cmd.mock.calls.some((c) => (c as unknown as string[])[0].includes('offset=50'))).toBe(true));
    const row = await screen.findByText('Rows 51–100');
    expect(within(row.parentElement!).getByRole('button', {name: 'Previous'})).toBeTruthy();
  });

  it('shows an empty state when there are no commands', async () => {
    routeFetch({'GET /api/audit/commands': {commands: [], limit: 50, offset: 0}});
    renderAt('/audit');
    expect(await screen.findByText('No commands found')).toBeTruthy();
  });
});

describe('navigation', () => {
  it('redirects / to /health and switches views via tabs', async () => {
    routeFetch({'GET /api/health': health, 'GET /api/systemd/units': {units: []}});
    renderAt('/');
    expect(await screen.findByText('Nginx')).toBeTruthy();
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', {name: 'Services'}));
    expect(await screen.findByText('No matching services')).toBeTruthy();
  });
});
