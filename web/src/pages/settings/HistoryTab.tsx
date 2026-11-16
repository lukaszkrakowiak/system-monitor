import {useCallback, useState} from 'react';
import {Badge} from '@astryxdesign/core/Badge';
import {Button} from '@astryxdesign/core/Button';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {HStack} from '@astryxdesign/core/HStack';
import {Spinner} from '@astryxdesign/core/Spinner';
import {Table} from '@astryxdesign/core/Table';
import {Text} from '@astryxdesign/core/Text';
import {VStack} from '@astryxdesign/core/VStack';
import {ErrorView} from '../../components/ErrorView';
import {getAlertHistory} from '../../lib/api';
import {formatTime} from '../../lib/format';
import {usePolling} from '../../lib/usePolling';
import type {NotificationInfo} from '../../types/api';

const PAGE_SIZE = 50;

const KIND_LABEL: Record<string, string> = {
  login: 'Suspicious login',
  service_state: 'Service problem',
  service_recovery: 'Service recovered',
  test: 'Test',
};

function deliveryTone(status: string): 'success' | 'warning' | 'error' | 'neutral' {
  if (status === 'sent') return 'success';
  if (status === 'pending') return 'warning';
  if (status === 'failed') return 'error';
  return 'neutral';
}

export function HistoryTab() {
  const [offset, setOffset] = useState(0);
  const fetcher = useCallback(() => getAlertHistory({limit: PAGE_SIZE, offset}), [offset]);
  const {data, error, loading} = usePolling(fetcher, 15_000);
  if (loading && !data) return <Spinner label="Loading history" />;
  if (!data) return error ? <ErrorView error={error} /> : null;
  if (data.notifications.length === 0 && offset === 0) {
    return <EmptyState title="No notifications yet" description="Notifications created by your alert rules will appear here." isCompact />;
  }
  return (
    <VStack gap={3}>
      <div className="scroll-x">
        <Table<NotificationInfo>
          data={data.notifications}
          idKey="id"
          density="compact"
          columns={[
            {key: 'created_at', header: 'Time', renderCell: (n) => formatTime(n.created_at)},
            {key: 'kind', header: 'Type', renderCell: (n) => KIND_LABEL[n.kind] ?? n.kind},
            {key: 'subject', header: 'Subject', renderCell: (n) => n.subject},
            {key: 'connector', header: 'Via', renderCell: (n) => n.connector},
            {key: 'status', header: 'Delivery', renderCell: (n) => <Badge variant={deliveryTone(n.status)} label={n.status} />},
            {
              key: 'last_error',
              header: 'Details',
              renderCell: (n) =>
                n.status === 'sent' ? formatTime(n.sent_at) : n.last_error ? `attempt ${n.attempts}: ${n.last_error}` : '—',
            },
          ]}
        />
      </div>
      <HStack gap={2} align="center">
        <Button label="Previous" variant="secondary" isDisabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))} />
        <Text type="supporting">
          {data.notifications.length === 0 ? 'No rows' : `Rows ${offset + 1}–${offset + data.notifications.length}`}
        </Text>
        <Button label="Next" variant="secondary" isDisabled={data.notifications.length < PAGE_SIZE} onClick={() => setOffset(offset + PAGE_SIZE)} />
      </HStack>
    </VStack>
  );
}
