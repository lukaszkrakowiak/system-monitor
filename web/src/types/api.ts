// Kontrakt REST API — MUSI być w 100% zgodny z internal/api/dto.go, internal/collector (payloady)
// oraz docs/SPEC.md (sekcje 4 i 6). Zgodność kluczy pilnuje test Go internal/api/contract_test.go.

export type Status = 'OK' | 'WARN' | 'CRIT';
export type GroupStatus = 'OK' | 'DEGRADED' | 'DOWN';

export type SystemdUnitHealth = {
  unit: string;
  display_name: string;
  active: string;
  sub: string;
  status: Status;
  memory_bytes: number | null;
  uptime: string;
};

export type SupervisorWorker = {
  name: string;
  state: string;
  pid: number;
};

export type SupervisorGroup = {
  group: string;
  running: number;
  total: number;
  status: GroupStatus;
  workers: SupervisorWorker[];
};

export type ElasticsearchHealth = {
  status: Status;
  cluster_name: string;
  cluster_status: string;
  nodes: number;
  unassigned_shards: number;
  active_shards_percent: number;
  error?: string;
};

export type RedisHealth = {
  status: Status;
  ping_ms: number;
  used_memory_human: string;
  connected_clients: number;
  uptime_in_seconds: number;
  error?: string;
};

export type KibanaHealth = {
  status: Status;
  overall_level: string;
  response_ms: number;
  error?: string;
};

export type Clusters = {
  elasticsearch?: ElasticsearchHealth;
  redis?: RedisHealth;
  kibana?: KibanaHealth;
};

export type HealthResponse = {
  overall_status: Status;
  generated_at: string;
  systemd: SystemdUnitHealth[];
  supervisor: SupervisorGroup[];
  clusters: Clusters;
};

export type UnitInfo = {
  unit: string;
  description: string;
  load: string;
  active: string;
  sub: string;
  selected: boolean;
};

export type UnitsResponse = {
  units: UnitInfo[];
};

export type SelectionRequest = {
  units: string[];
};

export type SessionInfo = {
  id: number;
  username: string;
  remote_ip: string;
  tty: string;
  pam_service: string;
  login_at: string;
  logout_at: string | null;
};

export type SessionsResponse = {
  sessions: SessionInfo[];
  limit: number;
  offset: number;
};

export type CommandInfo = {
  id: number;
  auid: number;
  login_username: string;
  effective_username: string;
  cwd: string;
  command: string;
  exit_code: number | null;
  executed_at: string;
};

export type CommandsResponse = {
  commands: CommandInfo[];
  limit: number;
  offset: number;
};

export type LoginFailureInfo = {
  id: number;
  username: string;
  remote_ip: string;
  service: string;
  attempted_at: string;
};

export type LoginFailuresResponse = {
  failures: LoginFailureInfo[];
  limit: number;
  offset: number;
};

export type ErrorResponse = {
  error: string;
};
