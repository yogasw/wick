// Mirrors internal/tools/agents/memory_handler.go. Field names are the
// JSON tags there — keep them in sync or the page silently renders zeros.

export interface AgentRow {
  name: string;
  pid: number;
  tree_bytes: number;
  largest_name?: string;
  largest_bytes?: number;
  procs: number;
  cpu_pct: number;
  io_read_bps: number;
  io_write_bps: number;
  peak_bytes?: number;
  peak_cpu_pct?: number;
  processes?: ProcessRow[];
}

export interface ProcessRow {
  pid: number;
  ppid: number;
  name: string;
  rss_bytes: number;
}

export interface DiskRow {
  path: string;
  total_bytes: number;
  free_bytes: number;
  avail_bytes: number;
  used_bytes: number;
  used_pct: number;
  known: boolean;
}

export interface CurrentLimits {
  agent_memory_max_mb: number;
  agents_total_memory_mb: number;
  tool_memory_max_mb: number;
  min_free_memory_mb: number;
}

export interface SuggestedLimits {
  AgentsTotalMB: number;
  AgentMaxMB: number;
  ToolMaxMB: number;
  MinFreeMB: number;
}

export interface HistoryStats {
  agent_points: number;
  machine_points: number;
  retention_sec: number;
  max_points: number;
  oldest_at?: string;
  span_sec: number;
}

export interface MemoryReport {
  scopes_available: boolean;
  notice?: string;
  mode: string;
  method: string;
  total_bytes?: number;
  available_bytes?: number;
  machine_known: boolean;
  agents: AgentRow[];
  processes_readable: boolean;
  suggested: SuggestedLimits;
  current: CurrentLimits;
  history: HistoryStats;
  disk: DiskRow;
}

export interface MachineSample {
  at: string;
  total_bytes: number;
  available_bytes: number;
  agent_bytes: number;
  agent_cpu_pct: number;
  agent_procs: number;
}

export interface AgentSample {
  at: string;
  provider: string;
  pid: number;
  rss_bytes: number;
  cpu_pct: number;
  io_read_bps: number;
  io_write_bps: number;
  procs: number;
}

export interface SeriesResponse {
  enabled: boolean;
  stats: HistoryStats;
  machine: MachineSample[];
  agents: AgentSample[];
}
