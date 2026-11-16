import {useState} from 'react';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {CheckboxInput} from '@astryxdesign/core/CheckboxInput';
import {Heading} from '@astryxdesign/core/Heading';
import {HStack} from '@astryxdesign/core/HStack';
import {NumberInput} from '@astryxdesign/core/NumberInput';
import {Selector} from '@astryxdesign/core/Selector';
import {Spinner} from '@astryxdesign/core/Spinner';
import {Switch} from '@astryxdesign/core/Switch';
import {Text} from '@astryxdesign/core/Text';
import {TextArea} from '@astryxdesign/core/TextArea';
import {TextInput} from '@astryxdesign/core/TextInput';
import {VStack} from '@astryxdesign/core/VStack';
import {ErrorView} from '../../components/ErrorView';
import {getConnectors, putConnector, testConnector} from '../../lib/api';
import {usePolling} from '../../lib/usePolling';
import type {ConnectorInfo} from '../../types/api';

const SECURITY = ['starttls', 'tls', 'none'];

type Notice = {status: 'success' | 'error' | 'warning'; title: string; description?: string};

const str = (v: unknown, d = ''): string => (typeof v === 'string' ? v : d);
const num = (v: unknown, d: number): number => (typeof v === 'number' ? v : d);
const strs = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);

/** Rozdziela adresy wpisane po jednym w linii lub po przecinku/średniku. */
export function splitList(s: string): string[] {
  return s
    .split(/[\n,;]+/)
    .map((x) => x.trim())
    .filter((x) => x !== '');
}

function EmailForm({info, secretStorage, onSaved}: {info: ConnectorInfo; secretStorage: boolean; onSaved: () => void}) {
  const c = info.config;
  const [enabled, setEnabled] = useState(info.enabled);
  const [host, setHost] = useState(str(c.host));
  const [port, setPort] = useState<number>(num(c.port, 587));
  const [security, setSecurity] = useState(str(c.security, 'starttls'));
  const [username, setUsername] = useState(str(c.username));
  const [from, setFrom] = useState(str(c.from));
  const [to, setTo] = useState(strs(c.to).join('\n'));
  const [password, setPassword] = useState('');
  const [clearPassword, setClearPassword] = useState(false);
  const [hasSecret, setHasSecret] = useState(info.has_secret);
  const [busy, setBusy] = useState<'save' | 'test' | null>(null);
  const [notice, setNotice] = useState<Notice | null>(null);

  async function save() {
    setBusy('save');
    setNotice(null);
    try {
      // null = zostaw zapisane hasło, "" = usuń, niepusty = ustaw nowe
      const pw = clearPassword ? '' : password !== '' ? password : null;
      const saved = await putConnector(info.type, {
        enabled,
        password: pw,
        config: {host: host.trim(), port, security, username: username.trim(), from: from.trim(), to: splitList(to)},
      });
      setHasSecret(saved.has_secret);
      setPassword('');
      setClearPassword(false);
      setNotice({status: 'success', title: 'Connector saved'});
      onSaved();
    } catch (e) {
      setNotice({status: 'error', title: 'Connector was not saved', description: e instanceof Error ? e.message : 'Saving failed'});
    } finally {
      setBusy(null);
    }
  }

  async function sendTest() {
    setBusy('test');
    setNotice(null);
    try {
      const r = await testConnector(info.type);
      setNotice(
        r.ok
          ? {status: 'success', title: 'Test message sent', description: 'Check the recipients’ inbox.'}
          : {status: 'error', title: 'Test message failed', description: r.error},
      );
    } catch (e) {
      setNotice({status: 'error', title: 'Test message failed', description: e instanceof Error ? e.message : 'Request failed'});
    } finally {
      setBusy(null);
    }
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <VStack gap={3}>
        <HStack gap={3} align="center" wrap="wrap">
          <Heading level={3} type="display-3">
            {info.label}
          </Heading>
          <Switch label="Enabled" value={enabled} onChange={(v) => setEnabled(v)} />
        </HStack>
        {notice && (
          <Banner status={notice.status} title={notice.title} description={notice.description} isDismissable onDismiss={() => setNotice(null)} />
        )}
        <HStack gap={2} align="end" wrap="wrap">
          <TextInput label="SMTP host" value={host} onChange={(v) => setHost(v)} width={280} />
          <NumberInput label="Port" value={port} onChange={(v) => setPort(v)} min={1} max={65535} width={120} />
          <Selector label="Security" options={SECURITY} value={security} onChange={(v) => setSecurity(v)} width={160} />
        </HStack>
        <HStack gap={2} align="end" wrap="wrap">
          <TextInput label="Username (optional)" value={username} onChange={(v) => setUsername(v)} width={280} />
          <TextInput
            label={hasSecret && !clearPassword ? 'Password (saved — leave empty to keep)' : 'Password'}
            type="password"
            value={password}
            onChange={(v) => setPassword(v)}
            isDisabled={!secretStorage || clearPassword}
            width={320}
          />
          {hasSecret && (
            <CheckboxInput label="Remove saved password" value={clearPassword} onChange={(v) => setClearPassword(v)} />
          )}
        </HStack>
        {!secretStorage && (
          <Banner
            status="warning"
            title="Password storage is disabled"
            description="Set SM_SECRET_KEY on the server (at least 32 characters) to store SMTP passwords. Without it only servers that need no password can be used."
          />
        )}
        <TextInput label="From" value={from} onChange={(v) => setFrom(v)} width={420} description='e.g. Server Monitor <monitor@example.org>' />
        <TextArea label="Recipients (one per line)" value={to} onChange={(v) => setTo(v)} rows={3} width={420} />
        <HStack gap={2}>
          <Button type="submit" label="Save" variant="primary" isLoading={busy === 'save'} isDisabled={busy !== null} />
          <Button
            label="Send test email"
            variant="secondary"
            isLoading={busy === 'test'}
            isDisabled={busy !== null || !info.configured}
            onClick={() => void sendTest()}
          />
        </HStack>
        <Text type="supporting">
          {info.configured
            ? 'The test uses the saved settings. Save first if you changed anything.'
            : 'Save the connector first, then you can send a test email.'}
        </Text>
      </VStack>
    </form>
  );
}

export function ConnectorsTab() {
  const {data, error, loading, reload} = usePolling(getConnectors, 600_000);
  if (loading && !data) return <Spinner label="Loading connectors" />;
  if (!data) return error ? <ErrorView error={error} /> : null;
  return (
    <VStack gap={5}>
      <Text type="supporting">
        Connectors are the channels notifications are sent through. Choose which ones are used in the Alert rules tab.
      </Text>
      {data.connectors.map((c) =>
        c.type === 'email' ? (
          <EmailForm key={c.type} info={c} secretStorage={data.secret_storage} onSaved={reload} />
        ) : (
          <Text key={c.type}>{c.label} is not supported by this version of the interface.</Text>
        ),
      )}
    </VStack>
  );
}
