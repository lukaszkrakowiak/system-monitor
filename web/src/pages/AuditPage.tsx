import {useCallback, useState} from 'react';
import {Button} from '@astryxdesign/core/Button';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {HStack} from '@astryxdesign/core/HStack';
import {Spinner} from '@astryxdesign/core/Spinner';
import {Tab, TabList} from '@astryxdesign/core/TabList';
import {Table} from '@astryxdesign/core/Table';
import {Text} from '@astryxdesign/core/Text';
import {TextInput} from '@astryxdesign/core/TextInput';
import {VStack} from '@astryxdesign/core/VStack';
import {ErrorView} from '../components/ErrorView';
import {IpCell, useIpLabels, type IpLabels} from '../components/IpCell';
import {getCommands, getLoginFailures, getSessions} from '../lib/api';
import {formatTime} from '../lib/format';
import {usePolling} from '../lib/usePolling';
import type {CommandInfo, LoginFailureInfo, SessionInfo} from '../types/api';

const PAGE_SIZE = 50;

type Filters = {user: string; q: string; ip: string};
const NO_FILTERS: Filters = {user: '', q: '', ip: ''};
type AuditTab = 'commands' | 'sessions' | 'failures';

function Pager({offset, count, onChange}: {offset: number; count: number; onChange: (o: number) => void}) {
  return (
    <HStack gap={2} align="center">
      <Button label="Previous" variant="secondary" isDisabled={offset === 0} onClick={() => onChange(Math.max(0, offset - PAGE_SIZE))} />
      <Text type="supporting">
        {count === 0 ? 'No rows' : `Rows ${offset + 1}–${offset + count}`}
      </Text>
      <Button label="Next" variant="secondary" isDisabled={count < PAGE_SIZE} onClick={() => onChange(offset + PAGE_SIZE)} />
    </HStack>
  );
}

function FilterBar({withPhrase = false, withIp = false, onApply}: {withPhrase?: boolean; withIp?: boolean; onApply: (f: Filters) => void}) {
  const [user, setUser] = useState('');
  const [q, setQ] = useState('');
  const [ip, setIp] = useState('');
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onApply({user: user.trim(), q: q.trim(), ip: ip.trim()});
      }}
    >
      <HStack gap={2} align="end" wrap="wrap">
        <TextInput label="User" value={user} onChange={(v) => setUser(v)} width={200} />
        {withIp && <TextInput label="Remote IP" value={ip} onChange={(v) => setIp(v)} width={200} />}
        {withPhrase && <TextInput label="Command contains" value={q} onChange={(v) => setQ(v)} width={320} />}
        <Button type="submit" label="Filter" variant="primary" />
      </HStack>
    </form>
  );
}

type CommandRow = CommandInfo;
type SessionRow = SessionInfo;
type FailureRow = LoginFailureInfo;

function CommandsTab() {
  const [filters, setFilters] = useState<Filters>(NO_FILTERS);
  const [offset, setOffset] = useState(0);
  const fetcher = useCallback(
    () => getCommands({user: filters.user, q: filters.q, limit: PAGE_SIZE, offset}),
    [filters, offset],
  );
  const {data, error, loading} = usePolling(fetcher, 15_000);
  return (
    <VStack gap={3}>
      <FilterBar withPhrase onApply={(f) => { setOffset(0); setFilters(f); }} />
      {error && !data ? (
        <ErrorView error={error} />
      ) : loading && !data ? (
        <Spinner label="Loading commands" />
      ) : data && data.commands.length === 0 ? (
        <EmptyState title="No commands found" isCompact />
      ) : data ? (
        <>
          <div className="scroll-x">
            <Table<CommandRow>
              data={data.commands}
              idKey="id"
              density="compact"
              columns={[
                {key: 'executed_at', header: 'Time', renderCell: (c) => formatTime(c.executed_at)},
                {key: 'login_username', header: 'Login user', renderCell: (c) => c.login_username},
                {key: 'effective_username', header: 'Runs as', renderCell: (c) => c.effective_username},
                {key: 'cwd', header: 'Directory', renderCell: (c) => <span className="mono">{c.cwd}</span>},
                {key: 'command', header: 'Command', renderCell: (c) => <span className="mono">{c.command}</span>},
                {key: 'exit_code', header: 'Exit', align: 'end', renderCell: (c) => (c.exit_code === null ? '—' : c.exit_code)},
              ]}
            />
          </div>
          <Pager offset={data.offset} count={data.commands.length} onChange={setOffset} />
        </>
      ) : null}
    </VStack>
  );
}

function SessionsTab({labels}: {labels: IpLabels}) {
  const [filters, setFilters] = useState<Filters>(NO_FILTERS);
  const [offset, setOffset] = useState(0);
  const fetcher = useCallback(() => getSessions({user: filters.user, limit: PAGE_SIZE, offset}), [filters, offset]);
  const {data, error, loading} = usePolling(fetcher, 15_000);
  return (
    <VStack gap={3}>
      <FilterBar withPhrase={false} onApply={(f) => { setOffset(0); setFilters(f); }} />
      {error && !data ? (
        <ErrorView error={error} />
      ) : loading && !data ? (
        <Spinner label="Loading sessions" />
      ) : data && data.sessions.length === 0 ? (
        <EmptyState title="No SSH sessions found" isCompact />
      ) : data ? (
        <>
          <div className="scroll-x">
            <Table<SessionRow>
              data={data.sessions}
              idKey="id"
              density="compact"
              columns={[
                {key: 'username', header: 'User', renderCell: (s) => s.username},
                {key: 'remote_ip', header: 'Remote IP', renderCell: (s) => <IpCell ip={s.remote_ip} labels={labels} />},
                {key: 'tty', header: 'TTY', renderCell: (s) => s.tty || '—'},
                {key: 'pam_service', header: 'Service', renderCell: (s) => s.pam_service},
                {key: 'login_at', header: 'Login', renderCell: (s) => formatTime(s.login_at)},
                {key: 'logout_at', header: 'Logout', renderCell: (s) => (s.logout_at ? formatTime(s.logout_at) : 'active')},
              ]}
            />
          </div>
          <Pager offset={data.offset} count={data.sessions.length} onChange={setOffset} />
        </>
      ) : null}
    </VStack>
  );
}

function FailuresTab({labels}: {labels: IpLabels}) {
  const [filters, setFilters] = useState<Filters>(NO_FILTERS);
  const [offset, setOffset] = useState(0);
  const fetcher = useCallback(
    () => getLoginFailures({user: filters.user, ip: filters.ip, limit: PAGE_SIZE, offset}),
    [filters, offset],
  );
  const {data, error, loading} = usePolling(fetcher, 15_000);
  return (
    <VStack gap={3}>
      <FilterBar withIp onApply={(f) => { setOffset(0); setFilters(f); }} />
      {error && !data ? (
        <ErrorView error={error} />
      ) : loading && !data ? (
        <Spinner label="Loading failed logins" />
      ) : data && data.failures.length === 0 ? (
        <EmptyState title="No failed logins found" isCompact />
      ) : data ? (
        <>
          <div className="scroll-x">
            <Table<FailureRow>
              data={data.failures}
              idKey="id"
              density="compact"
              columns={[
                {key: 'attempted_at', header: 'Time', renderCell: (f) => formatTime(f.attempted_at)},
                {key: 'username', header: 'Tried user', renderCell: (f) => f.username},
                {key: 'remote_ip', header: 'Remote IP', renderCell: (f) => <IpCell ip={f.remote_ip} labels={labels} />},
                {key: 'service', header: 'Service', renderCell: (f) => f.service || '—'},
              ]}
            />
          </div>
          <Pager offset={data.offset} count={data.failures.length} onChange={setOffset} />
        </>
      ) : null}
    </VStack>
  );
}

export function AuditPage() {
  const [tab, setTab] = useState<AuditTab>('commands');
  const labels = useIpLabels();
  return (
    <VStack gap={3}>
      <TabList role="tablist" value={tab} onChange={(v) => setTab(v as AuditTab)}>
        <Tab value="commands" label="CLI history" />
        <Tab value="sessions" label="SSH sessions" />
        <Tab value="failures" label="Failed logins" />
      </TabList>
      {tab === 'commands' ? <CommandsTab /> : tab === 'sessions' ? <SessionsTab labels={labels} /> : <FailuresTab labels={labels} />}
    </VStack>
  );
}
