import type {GroupStatus, HealthResponse, Status} from '../types/api';

export type BadgeTone = 'success' | 'warning' | 'error' | 'neutral';

export function statusTone(s: Status | GroupStatus | string): BadgeTone {
  switch (s) {
    case 'OK':
      return 'success';
    case 'WARN':
    case 'DEGRADED':
      return 'warning';
    case 'CRIT':
    case 'DOWN':
      return 'error';
  }
  return 'neutral';
}

export function formatBytes(n: number | null | undefined): string {
  if (n === null || n === undefined || n < 0) return '—';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`;
}

export function formatTime(iso: string | null | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

export function formatSeconds(s: number): string {
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

export type Summary = {
  systemd: {ok: number; total: number};
  supervisor: {ok: number; total: number};
  clusters: {ok: number; total: number};
};

export function summarize(h: HealthResponse): Summary {
  const clusters = Object.values(h.clusters).filter((c) => c !== undefined);
  return {
    systemd: {ok: h.systemd.filter((u) => u.status === 'OK').length, total: h.systemd.length},
    supervisor: {ok: h.supervisor.filter((g) => g.status === 'OK').length, total: h.supervisor.length},
    clusters: {ok: clusters.filter((c) => c.status === 'OK').length, total: clusters.length},
  };
}
