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
  DurationMs: number;
  Error: string;
  Message: string;
}

export interface SpawnDetailResponse {
  File: SpawnLogFileDTO;
  Events: SpawnEvent[];
  SessionDeleted: boolean;
  /** Masked reproduce commands keyed "<shell>-<h|i>-<full|short>-<res|new>". */
  Repro: Record<string, string>;
  /** True when the spawn had a resume id — the Keep/Fresh toggle is meaningful. */
  HasResume: boolean;
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
  Spawns: SpawnLogFileDTO[];
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
  Spawns: SpawnLogFileDTO[];
  Page: number;
  HasNext: boolean;
  Router9: Router9DetailDTO;
}

export interface Router9DetailDTO {
  Supported: boolean;
  Enabled: boolean;
  Models: Record<string, string>;
  KeySet: boolean;
}
