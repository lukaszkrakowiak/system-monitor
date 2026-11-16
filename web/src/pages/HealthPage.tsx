import {Link} from 'react-router-dom';
import {Banner} from '@astryxdesign/core/Banner';
import {Card} from '@astryxdesign/core/Card';
import {Collapsible} from '@astryxdesign/core/Collapsible';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {Heading} from '@astryxdesign/core/Heading';
import {HStack} from '@astryxdesign/core/HStack';
import {Spinner} from '@astryxdesign/core/Spinner';
import {Table} from '@astryxdesign/core/Table';
import {Text} from '@astryxdesign/core/Text';
import {VStack} from '@astryxdesign/core/VStack';
import {ErrorView} from '../components/ErrorView';
import {StatusBadge} from '../components/StatusBadge';
import {getHealth} from '../lib/api';
import {formatBytes, formatSeconds, formatTime, summarize} from '../lib/format';
import {usePolling} from '../lib/usePolling';
import type {
  ElasticsearchHealth,
  KibanaHealth,
  RedisHealth,
  SupervisorGroup,
  SystemdUnitHealth,
} from '../types/api';

export const HEALTH_REFRESH_MS = 10_000;

type UnitRow = SystemdUnitHealth;

function Tile({title, value, hint}: {title: string; value: React.ReactNode; hint?: string}) {
  return (
    <Card>
      <VStack gap={1}>
        <Text type="supporting">{title}</Text>
        <div>{value}</div>
        {hint && <Text type="supporting">{hint}</Text>}
      </VStack>
    </Card>
  );
}

function SystemdTable({units}: {units: SystemdUnitHealth[]}) {
  if (units.length === 0) {
    return (
      <EmptyState
        title="No services selected"
        description="Pick the systemd units to monitor."
        actions={<Link to="/services">Choose services</Link>}
        isCompact
      />
    );
  }
  return (
    <div className="scroll-x">
      <Table<UnitRow>
        data={units}
        idKey="unit"
        density="compact"
        columns={[
          {key: 'display_name', header: 'Service', renderCell: (u) => (
            <VStack>
              <Text weight="medium">{u.display_name || u.unit}</Text>
              <Text type="supporting">{u.unit}</Text>
            </VStack>
          )},
          {key: 'status', header: 'Status', renderCell: (u) => <StatusBadge status={u.status} />},
          {key: 'state', header: 'State', renderCell: (u) => `${u.active}${u.sub ? ` / ${u.sub}` : ''}`},
          {key: 'memory_bytes', header: 'Memory', align: 'end', renderCell: (u) => formatBytes(u.memory_bytes)},
          {key: 'uptime', header: 'Uptime', align: 'end', renderCell: (u) => u.uptime || '—'},
        ]}
      />
    </div>
  );
}

function SupervisorCards({groups}: {groups: SupervisorGroup[]}) {
  if (groups.length === 0) {
    return <Text type="supporting">No supervisor groups reported.</Text>;
  }
  return (
    <div className="cards">
      {groups.map((g) => (
        <Card key={g.group}>
          <VStack gap={2}>
            <HStack justify="between" align="center">
              <Text weight="semibold">{g.group}</Text>
              <StatusBadge status={g.status} />
            </HStack>
            <Text type="supporting">
              {g.running} / {g.total} running
            </Text>
            <Collapsible trigger={<Text>Workers ({g.workers.length})</Text>}>
              <VStack gap={1}>
                {g.workers.map((w) => (
                  <HStack key={w.name} justify="between">
                    <Text type="code">{w.name}</Text>
                    <Text type="supporting">
                      {w.state}
                      {w.pid > 0 ? ` · pid ${w.pid}` : ''}
                    </Text>
                  </HStack>
                ))}
              </VStack>
            </Collapsible>
          </VStack>
        </Card>
      ))}
    </div>
  );
}

function ClusterCard({title, status, error, rows}: {title: string; status: string; error?: string; rows: [string, React.ReactNode][]}) {
  return (
    <Card>
      <VStack gap={2}>
        <HStack justify="between" align="center">
          <Heading level={3} type="display-3">
            {title}
          </Heading>
          <StatusBadge status={status} />
        </HStack>
        {error && <Banner status="error" title="Probe failed" description={error} />}
        {!error &&
          rows.map(([k, v]) => (
            <HStack key={k} justify="between">
              <Text type="supporting">{k}</Text>
              <Text>{v}</Text>
            </HStack>
          ))}
      </VStack>
    </Card>
  );
}

function ElasticsearchCard({c}: {c: ElasticsearchHealth}) {
  return (
    <ClusterCard
      title="Elasticsearch"
      status={c.status}
      error={c.error}
      rows={[
        ['Cluster', c.cluster_name],
        ['State', c.cluster_status],
        ['Nodes', c.nodes],
        ['Unassigned shards', c.unassigned_shards],
        ['Active shards', `${c.active_shards_percent}%`],
      ]}
    />
  );
}

function RedisCard({c}: {c: RedisHealth}) {
  return (
    <ClusterCard
      title="Redis"
      status={c.status}
      error={c.error}
      rows={[
        ['Ping', `${c.ping_ms} ms`],
        ['Memory', c.used_memory_human],
        ['Clients', c.connected_clients],
        ['Uptime', formatSeconds(c.uptime_in_seconds)],
      ]}
    />
  );
}

function KibanaCard({c}: {c: KibanaHealth}) {
  return (
    <ClusterCard
      title="Kibana"
      status={c.status}
      error={c.error}
      rows={[
        ['Level', c.overall_level],
        ['Response', `${c.response_ms} ms`],
      ]}
    />
  );
}

export function HealthPage() {
  const {data, error, loading} = usePolling(getHealth, HEALTH_REFRESH_MS);

  if (loading && !data) return <Spinner label="Loading health" />;
  if (!data) return error ? <ErrorView error={error} /> : null;

  const s = summarize(data);
  const {elasticsearch, redis, kibana} = data.clusters;
  return (
    <VStack gap={5}>
      {error && <Banner status="warning" title="Refresh failed — showing last data" description={error.message} />}

      <div className="tiles">
        <Tile title="Overall" value={<StatusBadge status={data.overall_status} />} hint={`Updated ${formatTime(data.generated_at)}`} />
        <Tile title="Systemd" value={<Text size="2xl" weight="semibold">{s.systemd.ok} / {s.systemd.total}</Text>} hint="services OK" />
        <Tile title="Supervisor" value={<Text size="2xl" weight="semibold">{s.supervisor.ok} / {s.supervisor.total}</Text>} hint="groups OK" />
        <Tile title="Clusters" value={<Text size="2xl" weight="semibold">{s.clusters.ok} / {s.clusters.total}</Text>} hint="probes OK" />
      </div>

      <VStack gap={2}>
        <Heading level={2} type="display-3">Systemd services</Heading>
        <SystemdTable units={data.systemd} />
      </VStack>

      <VStack gap={2}>
        <Heading level={2} type="display-3">Supervisor workers</Heading>
        <SupervisorCards groups={data.supervisor} />
      </VStack>

      <VStack gap={2}>
        <Heading level={2} type="display-3">Clusters</Heading>
        {elasticsearch || redis || kibana ? (
          <div className="cards">
            {elasticsearch && <ElasticsearchCard c={elasticsearch} />}
            {redis && <RedisCard c={redis} />}
            {kibana && <KibanaCard c={kibana} />}
          </div>
        ) : (
          <Text type="supporting">No cluster probes configured.</Text>
        )}
      </VStack>
    </VStack>
  );
}
