export interface ProviderCapDTO {
  Used: number;
  Max: number;
  Unlimited: boolean;
}

export interface LiveProcessDTO {
  SessionID: string;
  AgentName: string;
  PID: number;
  Lifecycle: string;
  Substate: string;
}

export interface HookCapabilityDTO {
  Supported: boolean;
  Verified: boolean;
  ProbedAt: string;
  Error: string;
  Scope: string;
}

export interface ProviderInstanceDTO {
  Type: string;
  Name: string;
  Binary: string;
  Disabled: boolean;
  MaxConcurrent: number;
  SendMode: string;
}

export interface ProviderStatusDTO {
  Instance: ProviderInstanceDTO;
  Path: string;
  PathFound: boolean;
  Version: string;
  VersionErr: string;
  Probing: boolean;
  Hooks: Record<string, HookCapabilityDTO>;
  Cap: ProviderCapDTO;
  HookEnabled: Record<string, boolean>;
}

export interface SpawnLogFileDTO {
  Path: string;
  ProviderType: string;
  ProviderName: string;
  SessionID: string;
  StartedAt: string;
  PID: number;
  Origin: string;
  FirstUserMessage: string;
  Binary: string;
  ExitReason: string;
  ReasonDetail: string;
  ExitCode: number;
  StderrTail: string;
}

/** Paged Recent Spawns result from GET /api/providers/spawns. */
export interface SpawnsList {
  Spawns: SpawnLogFileDTO[];
  Page: number;
  HasNext: boolean;
  Total: number;
}

/** One session row in the per-session Recent Spawns list. */
export interface SessionSummary {
  SessionID: string;
  ProviderType: string;
  ProviderName: string;
  SpawnCount: number;
  LastStatus: string;
  LastStarted: string;
  FirstMessage: string;
  Origin: string;
}

export interface SessionsList {
  Sessions: SessionSummary[];
  Page: number;
  HasNext: boolean;
  Total: number;
}

/** Every spawn of one session (session detail page). */
export interface SessionSpawns {
  SessionID: string;
  ProviderType: string;
  ProviderName: string;
  Spawns: SpawnLogFileDTO[];
}

/** Tail of one runtime log file (log viewer). */
export interface LogTail {
  Name: string;
  Path: string;
  Size: number;
  Content: string;
  Truncated: boolean;
  Modified: string;
}

export interface SpawnEvent {
  Type: string;
  At: string;
  ProviderType: string;
  ProviderName: string;
  AgentName: string;
  Workspace: string;
  ResumeID: string;
  Binary: string;
  Args: string[];
  Env: string[];
  PID: number;
  Origin: string;
  FirstUserMessage: string;
  ExitReason: string;
  ReasonDetail: string;
  ExitCode: number;
  StderrTail: string;
  DurationMs: number;
  Error: string;
  Message: string;
}

export interface LogRef {
  Prefix: string;
  Path: string;
}

export interface SpawnWindow {
  Start: string;
  End: string;
  DurationMs: number;
  Running: boolean;
  /** Died without an exit event (crash / OS-kill); End is approximate. */
  Unclean: boolean;
}

export interface SpawnLogsDTO {
  /** Full path to the spawn's own jsonl (event timeline incl. crash stderr). */
  SpawnPath: string;
  /** Absolute logs dir, for display. */
  LogsDir: string;
  /** Process logs (app/server/worker/mcp/gate/daemon) from the spawn day(s). */
  Components: LogRef[];
  /** The spawn's start→end window, to scan the process logs. */
  Window: SpawnWindow;
  /** Total .log files in the logs dir (any date); 0 = no process logs
      written at all (dev/console mode). Lets the UI explain an empty list. */
  LogsPresent: number;
}

export interface SpawnDetailResponse {
  File: SpawnLogFileDTO;
  Events: SpawnEvent[];
  SessionDeleted: boolean;
  /** Masked reproduce commands keyed "<shell>-<h|i>-<full|short>-<res|new>". */
  Repro: Record<string, string>;
  /** True when the spawn had a resume id — the Keep/Fresh toggle is meaningful. */
  HasResume: boolean;
  Logs: SpawnLogsDTO;
}

export interface MCPClientDTO {
  ID: string;
  Label: string;
  Detected: boolean;
  Installed: boolean;
  Blocklisted: boolean;
  ConfigPath: string;
}

export interface MCPStatusDTO {
  AppName: string;
  Clients: MCPClientDTO[];
}

export interface GateStatusDTO {
  Enabled: boolean;
  Binary: string;
  Source: string;
  Reason: string;
  Note: string;
  PermissionMode: string;
  BypassLocked: boolean;
}

export interface ProvidersListResponse {
  Providers: ProviderStatusDTO[];
  Gate: GateStatusDTO;
  MCPClients: MCPStatusDTO;
  AutoRescan: boolean;
  PoolActive: number;
  PoolQueueLen: number;
  PoolMax: number;
  LiveProcesses: LiveProcessDTO[];
  SupportedKeys: string[];
}

export interface ConfigFieldDTO {
  Key: string;
  Value: string;
  Type: string;
  Options: string;
  IsSecret: boolean;
  Description: string;
  Required: boolean;
}

export interface StorageFileDTO {
  id: number;
  provider_type: string;
  instance_name: string;
  rel_path: string;
  name: string;
  is_dir: boolean;
  size: number;
  synced_at: string;
  retention_days: number;
}

export interface StorageResponse {
  files: StorageFileDTO[];
  filter_provider: string;
  filter_instance: string;
  provider_types: string[];
}

export interface ProviderDetailResponse {
  Instance: ProviderInstanceDTO;
  Path: string;
  PathFound: boolean;
  Version: string;
  VersionErr: string;
  Probing: boolean;
  Hooks: Record<string, HookCapabilityDTO>;
  HookEnabled: Record<string, boolean>;
  Gate: GateStatusDTO;
  GlobalMax: number;
  ActiveCount: number;
  ActivePIDs: LiveProcessDTO[];
  ConfigFields: ConfigFieldDTO[];
  AIRouter: AIRouterDetailDTO;
}

export interface AIRouterDetailDTO {
  Supported: boolean;
  Enabled: boolean;
  Provider: string;
  Routers: { ID: string; Name: string }[];
  Models: Record<string, string>;
  KeySet: boolean;
  RawConfig: string;
  Preview: string;
}
