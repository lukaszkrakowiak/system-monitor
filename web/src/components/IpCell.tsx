import {useCallback, useMemo, useState} from 'react';
import {Badge} from '@astryxdesign/core/Badge';
import {Button} from '@astryxdesign/core/Button';
import {HStack} from '@astryxdesign/core/HStack';
import {Text} from '@astryxdesign/core/Text';
import {TextInput} from '@astryxdesign/core/TextInput';
import {deleteIPLabel, getIPLabels, putIPLabel} from '../lib/api';
import {usePolling} from '../lib/usePolling';

export const MAX_LABEL_LEN = 64;

export type IpLabels = {
  /** label for the (possibly non-canonical) IP, or undefined */
  get: (ip: string) => string | undefined;
  save: (ip: string, label: string) => Promise<void>;
  remove: (ip: string) => Promise<void>;
};

/** Mirrors storage.NormalizeIP on the server: IPv4-mapped IPv6 collapses to IPv4, IPv6 is lowercase. */
export function normalizeIp(ip: string): string {
  const s = ip.trim().toLowerCase();
  const m = /^::ffff:(\d{1,3}(?:\.\d{1,3}){3})$/.exec(s);
  return m ? m[1] : s;
}

/**
 * Loads the user's IP labels. Labels are a convenience: when they cannot be loaded
 * the pages keep working and simply show bare IPs.
 */
export function useIpLabels(): IpLabels {
  const fetcher = useCallback(() => getIPLabels(), []);
  const {data, reload} = usePolling(fetcher, 60_000);
  const map = useMemo(() => new Map((data?.labels ?? []).map((l) => [l.ip, l.label])), [data]);
  return useMemo(
    () => ({
      get: (ip) => map.get(normalizeIp(ip)),
      save: async (ip, label) => {
        await putIPLabel(normalizeIp(ip), label);
        reload();
      },
      remove: async (ip) => {
        await deleteIPLabel(normalizeIp(ip));
        reload();
      },
    }),
    [map, reload],
  );
}

/** An IP address with its user-defined label and inline editing. */
export function IpCell({ip, labels}: {ip: string; labels: IpLabels}) {
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!ip) return <>—</>;
  const label = labels.get(ip);

  const start = () => {
    setValue(label ?? '');
    setError(null);
    setEditing(true);
  };
  const run = async (op: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await op();
      setEditing(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Saving failed');
    } finally {
      setBusy(false);
    }
  };
  const save = () => {
    const v = value.trim();
    if (!v) {
      setError('Label must not be empty');
      return;
    }
    if ([...v].length > MAX_LABEL_LEN) {
      setError(`Label is too long (max ${MAX_LABEL_LEN} characters)`);
      return;
    }
    void run(() => labels.save(ip, v));
  };

  if (editing) {
    return (
      <div>
        <HStack gap={2} align="center" wrap="wrap">
          <span className="mono">{ip}</span>
          <TextInput
            label={`Label for ${ip}`}
            isLabelHidden
            placeholder="e.g. office, my laptop"
            value={value}
            onChange={(v) => setValue(v)}
            onEnter={save}
            width={200}
          />
          <Button label="Save" variant="primary" size="sm" isDisabled={busy} onClick={save} />
          {label !== undefined && (
            <Button label="Remove label" variant="destructive" size="sm" isDisabled={busy} onClick={() => void run(() => labels.remove(ip))} />
          )}
          <Button label="Cancel" variant="ghost" size="sm" isDisabled={busy} onClick={() => setEditing(false)} />
        </HStack>
        {error && <Text type="supporting">{error}</Text>}
      </div>
    );
  }
  return (
    <HStack gap={2} align="center" wrap="wrap">
      <span className="mono">{ip}</span>
      {label !== undefined && <Badge variant="neutral" label={label} />}
      <Button label={label !== undefined ? 'Edit label' : 'Add label'} variant="ghost" size="sm" onClick={start} />
    </HStack>
  );
}
