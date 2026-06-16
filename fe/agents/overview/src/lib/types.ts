export interface QueuedEntry {
  session_id: string;
  agent_name: string;
  waiting_ms: number;
  label: string;
  project: string;
}

export interface ActiveEntry {
  session_id: string;
  label: string;
  lifecycle: string;
  pid: number;
  project_id: string;
}

export interface OverviewStats {
  active: number;
  pool_max: number;
  queue_len: number;
}

export interface OverviewResponse {
  queued: QueuedEntry[];
  active: ActiveEntry[];
  stats: OverviewStats;
}
