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
  // "ok" | "warn" | "full" — graded server-side on percentage AND absolute
  // free space, so the page and the CLI cannot disagree about what counts
  // as alarming.
  pressure: string;
  known: boolean;
}

// Machine-wide process rows. Not scoped to wick: when the box is slow the
// cause is often not an agent at all.
export interface TopProcessRow {
  pid: number;
  name: string;
  // Full command (Linux) or image path (Windows). Identifies which of
  // several same-named processes this is. Empty for kernel threads and
  // processes owned by another user.
  cmdline?: string;
  rss_bytes: number;
  cpu_pct: number;
  io_read_bps: number;
  io_write_bps: number;
  // >1 when the row is a group of same-named processes. The summary
  // tables always group; the explorer's member rows do not.
  count?: number;
  // Why this row reads 0 B, when it does. Absent for ordinary processes.
  //
  //   kernel  a kernel thread — no user address space exists to measure
  //   zombie  already exited, waiting for its parent to reap it
  //
  // Neither is "using no memory": they have no memory to use, and
  // neither can be ended by a signal.
  kind?: "kernel" | "zombie";
}

export interface TopProcesses {
  available: boolean;
  total: number;
  by_memory: TopProcessRow[];
  by_cpu: TopProcessRow[];
  by_io: TopProcessRow[];
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
  // CPU figures are percent of ONE core, so the ceiling is cores × 100.
  cpu_cores: number;
  agents: AgentRow[];
  processes_readable: boolean;
  suggested: SuggestedLimits;
  current: CurrentLimits;
  history: HistoryStats;
  disk: DiskRow;
  top: TopProcesses;
}

export interface MachineSample {
  at: string;
  total_bytes: number;
  available_bytes: number;
  agent_bytes: number;
  agent_cpu_pct: number;
  agent_procs: number;
  // Machine-wide totals, so the chart has something to show when no agent
  // is running — which is exactly when someone asks why the box is slow.
  machine_used_bytes: number;
  machine_cpu_pct: number;
  machine_procs: number;
}

// Process explorer: grouped by executable, searchable, paginated.
export interface ProcessGroupRow {
  name: string;
  count: number;
  rss_bytes: number;
  cpu_pct: number;
  io_read_bps: number;
  io_write_bps: number;
  pct_of_machine_mem: number;
  members: TopProcessRow[];
  // Set only when every member agrees — see groupKind on the server. A
  // mixed group carries nothing, since the label would be untrue of half
  // its rows.
  kind?: "kernel" | "zombie";
}

export interface ProcessListResponse {
  available: boolean;
  total: number;
  matched: number;
  page: number;
  per_page: number;
  pages: number;
  machine_mem_bytes: number;
  // CPU figures are percent of ONE core, so the ceiling is cores × 100.
  // Shown in the header because 444% reads as a bug until you know that.
  cpu_cores: number;
  groups: ProcessGroupRow[];
  // This wick server. Its row cannot be ended — the server refuses — so
  // the UI marks it rather than offering a button that declines.
  self_pid: number;
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
