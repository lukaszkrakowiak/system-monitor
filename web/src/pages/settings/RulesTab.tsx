import {useState} from 'react';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {CheckboxInput} from '@astryxdesign/core/CheckboxInput';
import {Heading} from '@astryxdesign/core/Heading';
import {HStack} from '@astryxdesign/core/HStack';
import {NumberInput} from '@astryxdesign/core/NumberInput';
import {Spinner} from '@astryxdesign/core/Spinner';
import {Switch} from '@astryxdesign/core/Switch';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {TextInput} from '@astryxdesign/core/TextInput';
import {VStack} from '@astryxdesign/core/VStack';
import {ErrorView} from '../../components/ErrorView';
import {getAlertSettings, getConnectors, putAlertSettings} from '../../lib/api';
import {usePolling} from '../../lib/usePolling';
import type {AlertSettings, AlertWindow, ConnectorInfo} from '../../types/api';
import {splitList} from './ConnectorsTab';

const DAYS: {key: string; label: string}[] = [
  {key: 'mon', label: 'Mon'},
  {key: 'tue', label: 'Tue'},
  {key: 'wed', label: 'Wed'},
  {key: 'thu', label: 'Thu'},
  {key: 'fri', label: 'Fri'},
  {key: 'sat', label: 'Sat'},
  {key: 'sun', label: 'Sun'},
];

const PRESETS: {label: string; window: AlertWindow}[] = [
  {label: 'Weeknights (Mon–Fri 17:00–06:00)', window: {days: ['mon', 'tue', 'wed', 'thu', 'fri'], from: '17:00', to: '06:00'}},
  {label: 'Weekend (all day)', window: {days: ['sat', 'sun'], from: '00:00', to: '00:00'}},
];

type Notice = {status: 'success' | 'error'; title: string; description?: string};

function WindowEditor({w, onChange, onRemove}: {w: AlertWindow; onChange: (w: AlertWindow) => void; onRemove: () => void}) {
  const toggleDay = (day: string, on: boolean) =>
    onChange({...w, days: on ? [...w.days, day] : w.days.filter((d) => d !== day)});
  return (
    <HStack gap={3} align="end" wrap="wrap">
      {DAYS.map((d) => (
        <CheckboxInput key={d.key} label={d.label} value={w.days.includes(d.key)} onChange={(v) => toggleDay(d.key, v)} />
      ))}
      <TextInput label="From (HH:MM)" value={w.from} onChange={(v) => onChange({...w, from: v})} width={120} />
      <TextInput label="To (HH:MM)" value={w.to} onChange={(v) => onChange({...w, to: v})} width={120} />
      <Button label="Remove" variant="ghost" onClick={onRemove} />
    </HStack>
  );
}

function RulesForm({initial, connectors, onSaved}: {initial: AlertSettings; connectors: ConnectorInfo[]; onSaved: () => void}) {
  const [s, setS] = useState<AlertSettings>(initial);
  const [allowedText, setAllowedText] = useState(initial.login_ip.allowed.join('\n'));
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<Notice | null>(null);

  const setWindows = (windows: AlertWindow[]) => setS({...s, login_time: {...s.login_time, windows}});

  async function save() {
    setBusy(true);
    setNotice(null);
    try {
      const saved = await putAlertSettings({...s, login_ip: {...s.login_ip, allowed: splitList(allowedText)}});
      setS(saved);
      setAllowedText(saved.login_ip.allowed.join('\n'));
      setNotice({status: 'success', title: 'Alert rules saved'});
      onSaved();
    } catch (e) {
      setNotice({status: 'error', title: 'Rules were not saved', description: e instanceof Error ? e.message : 'Saving failed'});
    } finally {
      setBusy(false);
    }
  }

  const usable = connectors.filter((c) => c.configured && c.enabled);
  const toggleChannel = (type: string, on: boolean) =>
    setS({...s, channels: on ? [...s.channels, type] : s.channels.filter((c) => c !== type)});

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <VStack gap={5}>
        {notice && (
          <Banner status={notice.status} title={notice.title} description={notice.description} isDismissable onDismiss={() => setNotice(null)} />
        )}

        <VStack gap={2}>
          <Heading level={3} type="display-3">
            Where to send notifications
          </Heading>
          {connectors.map((c) => (
            <CheckboxInput
              key={c.type}
              label={c.configured && c.enabled ? c.label : `${c.label} (not enabled — set it up in Connectors)`}
              value={s.channels.includes(c.type)}
              isDisabled={!c.configured || !c.enabled}
              onChange={(v) => toggleChannel(c.type, v)}
            />
          ))}
          {usable.length === 0 && (
            <Banner status="info" title="No connector is enabled yet" description="Enable and save a connector first; without one, alerts are not sent." />
          )}
          <TextInput
            label="Time zone for the rules"
            value={s.timezone}
            onChange={(v) => setS({...s, timezone: v})}
            width={320}
            description="IANA name, e.g. Europe/Warsaw. Empty = server time zone."
          />
        </VStack>

        <VStack gap={3}>
          <HStack gap={3} align="center">
            <Heading level={3} type="display-3">
              SSH logins at certain times
            </Heading>
            <Switch label="Enabled" value={s.login_time.enabled} onChange={(v) => setS({...s, login_time: {...s.login_time, enabled: v}})} />
          </HStack>
          <Text type="supporting">
            Notify when someone logs in during any window below. A window applies to the selected days (by the calendar day of the login);
            if “From” is later than “To” it crosses midnight (17:00–06:00 means 17:00–24:00 and 00:00–06:00). From = To means the whole day.
          </Text>
          {s.login_time.windows.map((w, i) => (
            <WindowEditor
              key={i}
              w={w}
              onChange={(nw) => setWindows(s.login_time.windows.map((x, j) => (j === i ? nw : x)))}
              onRemove={() => setWindows(s.login_time.windows.filter((_, j) => j !== i))}
            />
          ))}
          <HStack gap={2} wrap="wrap">
            {PRESETS.map((p) => (
              <Button key={p.label} label={`Add: ${p.label}`} variant="secondary" onClick={() => setWindows([...s.login_time.windows, {...p.window}])} />
            ))}
            <Button label="Add empty window" variant="ghost" onClick={() => setWindows([...s.login_time.windows, {days: [], from: '17:00', to: '06:00'}])} />
          </HStack>
        </VStack>

        <VStack gap={3}>
          <HStack gap={3} align="center">
            <Heading level={3} type="display-3">
              SSH logins from unknown IPs
            </Heading>
            <Switch label="Enabled" value={s.login_ip.enabled} onChange={(v) => setS({...s, login_ip: {...s.login_ip, enabled: v}})} />
          </HStack>
          <TextArea
            label="Allowed addresses (one per line)"
            value={allowedText}
            onChange={(v) => setAllowedText(v)}
            rows={5}
            width={420}
            description="IP addresses or CIDR ranges, e.g. 192.0.2.10 or 10.0.0.0/8. A login from any other address triggers a notification. Local console logins are ignored."
          />
        </VStack>

        <VStack gap={3}>
          <HStack gap={3} align="center">
            <Heading level={3} type="display-3">
              Service state
            </Heading>
            <Switch label="Enabled" value={s.service_state.enabled} onChange={(v) => setS({...s, service_state: {...s.service_state, enabled: v}})} />
          </HStack>
          <Text type="supporting">
            Notify when a monitored service (systemd, supervisor group, Elasticsearch, Redis, Kibana) is in any state other than OK.
          </Text>
          <HStack gap={3} align="end" wrap="wrap">
            <NumberInput
              label="Confirm after N checks"
              value={s.service_state.confirm_checks}
              onChange={(v) => setS({...s, service_state: {...s.service_state, confirm_checks: v}})}
              min={1}
              max={10}
              width={180}
              description="Checks run every 10–15 s; 2 avoids alerts from a single failed probe."
            />
            <Switch
              label="Also notify when it recovers"
              value={s.service_state.notify_recovery}
              onChange={(v) => setS({...s, service_state: {...s.service_state, notify_recovery: v}})}
            />
          </HStack>
        </VStack>

        {busy ? <Spinner label="Saving" /> : <Button type="submit" label="Save rules" variant="primary" />}
      </VStack>
    </form>
  );
}

export function RulesTab() {
  const settings = usePolling(getAlertSettings, 600_000);
  const connectors = usePolling(getConnectors, 600_000);
  if ((settings.loading && !settings.data) || (connectors.loading && !connectors.data)) return <Spinner label="Loading alert rules" />;
  const err = settings.error ?? connectors.error;
  if (!settings.data || !connectors.data) return err ? <ErrorView error={err} /> : null;
  return <RulesForm initial={settings.data} connectors={connectors.data.connectors} onSaved={settings.reload} />;
}
