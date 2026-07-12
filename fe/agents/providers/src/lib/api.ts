import type {
  ProvidersListResponse,
  ProviderDetailResponse,
  StorageResponse,
  StorageFileDTO,
  ProviderStatusDTO,
  ProviderInstanceDTO,
  ProviderCapDTO,
  HookCapabilityDTO,
  SpawnLogFileDTO,
  SpawnEvent,
  SpawnDetailResponse,
  MCPClientDTO,
  MCPStatusDTO,
  GateStatusDTO,
  LiveProcessDTO,
  ConfigFieldDTO,
} from "./types.js";

interface WireProviderInstance {
  type: string;
  name: string;
  binary: string;
  disabled: boolean;
  max_concurrent: number;
  send_mode: string;
}

interface WireProviderCap {
  used: number;
  max: number;
  unlimited: boolean;
}

interface WireHookCapability {
  supported: boolean;
  verified: boolean;
  probed_at?: string;
  error?: string;
  scope?: string;
}

interface WireProviderStatus {
  instance: WireProviderInstance;
  path: string;
  path_found: boolean;
  version: string;
  version_err?: string;
  probing: boolean;
  hooks: Record<string, WireHookCapability> | null;
  cap: WireProviderCap;
  hook_enabled: Record<string, boolean> | null;
}

interface WireSpawnLogFile {
  path: string;
  provider_type: string;
  provider_name: string;
  session_id: string;
  started_at: string;
  pid?: number;
  origin?: string;
  first_user_message?: string;
  binary?: string;
  exit_reason?: string;
}

interface WireSpawnEvent {
  type: string;
  at: string;
  provider_type?: string;
  provider_name?: string;
  agent_name?: string;
  workspace?: string;
  resume_id?: string;
  binary?: string;
  args?: string[];
  env?: string[];
  pid?: number;
  origin?: string;
  first_user_message?: string;
  exit_reason?: string;
  duration_ms?: number;
  error?: string;
  message?: string;
}

interface WireSpawnDetailResponse {
  file: WireSpawnLogFile;
  events: WireSpawnEvent[] | null;
  session_deleted: boolean;
  repro: Record<string, string> | null;
  has_resume: boolean;
}

interface WireMCPClient {
  id: string;
  label: string;
  detected: boolean;
  installed: boolean;
  blocklisted: boolean;
  config_path: string;
}

interface WireMCPStatus {
  app_name: string;
  clients: WireMCPClient[] | null;
}

interface WireGateStatus {
  enabled: boolean;
  binary: string;
  source: string;
  reason?: string;
  note: string;
  permission_mode: string;
  bypass_locked: boolean;
}

interface WireLiveProcess {
  session_id: string;
  agent_name: string;
  pid: number;
  lifecycle: string;
  substate: string;
}

interface WireConfigField {
  key: string;
  value: string;
  type: string;
  options?: string;
  is_secret: boolean;
  description?: string;
  required: boolean;
}

interface WireProvidersListResponse {
  providers: WireProviderStatus[] | null;
  gate: WireGateStatus | null;
  spawns: WireSpawnLogFile[] | null;
  mcp: WireMCPStatus | null;
  auto_rescan: boolean;
  pool_active: number;
  pool_queue_len: number;
  pool_max: number;
  live_processes: WireLiveProcess[] | null;
  supported_keys: string[] | null;
}

interface WireProviderDetailResponse {
  instance: WireProviderInstance;
  path: string;
  path_found: boolean;
  version: string;
  version_err?: string;
  probing: boolean;
  hooks: Record<string, WireHookCapability> | null;
  hook_enabled: Record<string, boolean> | null;
  gate: WireGateStatus | null;
  global_max: number;
  active_count: number;
  active_pids: WireLiveProcess[] | null;
  config_fields: WireConfigField[] | null;
  spawns: WireSpawnLogFile[] | null;
  page: number;
  has_next: boolean;
  airouter?: {
    supported?: boolean;
    enabled?: boolean;
    provider?: string;
    routers?: { id: string; name: string }[] | null;
    models?: Record<string, string> | null;
    key_set?: boolean;
    raw_config?: string;
    preview?: string;
  } | null;
}

class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message);
  }
}

function getBase(): string {
  return document.getElementById("app")?.dataset.base ?? "";
}

async function get<T>(path: string): Promise<T> {
  const resp = await fetch(path, {
    credentials: "same-origin",
    headers: { "Accept": "application/json" },
  });
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new ApiError(resp.status, body || `HTTP ${resp.status}`);
  }
  return resp.json() as Promise<T>;
}

async function post<T>(path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method: "POST", redirect: "follow" };
  if (body !== undefined) {
    init.headers = { "Content-Type": "application/json", "Accept": "application/json" };
    init.body = JSON.stringify(body);
  } else {
    init.headers = { "Accept": "application/json" };
  }
  const resp = await fetch(path, init);
  if (resp.type === "opaqueredirect" || resp.status === 303) return undefined as unknown as T;
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    throw new ApiError(resp.status, text || `HTTP ${resp.status}`);
  }
  const ct = resp.headers.get("content-type") ?? "";
  if (ct.includes("application/json")) return resp.json() as Promise<T>;
  return undefined as unknown as T;
}

async function del<T>(path: string): Promise<T> {
  const resp = await fetch(path, {
    method: "DELETE",
    credentials: "same-origin",
    headers: { "Accept": "application/json" },
  });
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    throw new ApiError(resp.status, text || `HTTP ${resp.status}`);
  }
  const ct = resp.headers.get("content-type") ?? "";
  if (ct.includes("application/json")) return resp.json() as Promise<T>;
  return undefined as unknown as T;
}

function mapInstance(w: WireProviderInstance): ProviderInstanceDTO {
  return {
    Type: w.type ?? "",
    Name: w.name ?? "",
    Binary: w.binary ?? "",
    Disabled: w.disabled ?? false,
    MaxConcurrent: w.max_concurrent ?? 0,
    SendMode: w.send_mode ?? "",
  };
}

function mapCap(w: WireProviderCap | undefined): ProviderCapDTO {
  return {
    Used: w?.used ?? 0,
    Max: w?.max ?? 0,
    Unlimited: w?.unlimited ?? false,
  };
}

function mapHooks(w: Record<string, WireHookCapability> | null | undefined): Record<string, HookCapabilityDTO> {
  const out: Record<string, HookCapabilityDTO> = {};
  for (const [k, v] of Object.entries(w ?? {})) {
    out[k] = {
      Supported: v.supported ?? false,
      Verified: v.verified ?? false,
      ProbedAt: v.probed_at ?? "",
      Error: v.error ?? "",
      Scope: v.scope ?? "",
    };
  }
  return out;
}

function mapGate(w: WireGateStatus | null | undefined): GateStatusDTO {
  return {
    Enabled: w?.enabled ?? false,
    Binary: w?.binary ?? "",
    Source: w?.source ?? "",
    Reason: w?.reason ?? "",
    Note: w?.note ?? "",
    PermissionMode: w?.permission_mode ?? "",
    BypassLocked: w?.bypass_locked ?? false,
  };
}

function mapSpawn(w: WireSpawnLogFile): SpawnLogFileDTO {
  return {
    Path: w.path ?? "",
    ProviderType: w.provider_type ?? "",
    ProviderName: w.provider_name ?? "",
    SessionID: w.session_id ?? "",
    StartedAt: w.started_at ?? "",
    PID: w.pid ?? 0,
    Origin: w.origin ?? "",
    FirstUserMessage: w.first_user_message ?? "",
    Binary: w.binary ?? "",
    ExitReason: w.exit_reason ?? "",
  };
}

function mapMCPClient(w: WireMCPClient): MCPClientDTO {
  return {
    ID: w.id ?? "",
    Label: w.label ?? "",
    Detected: w.detected ?? false,
    Installed: w.installed ?? false,
    Blocklisted: w.blocklisted ?? false,
    ConfigPath: w.config_path ?? "",
  };
}

function mapMCP(w: WireMCPStatus | null | undefined): MCPStatusDTO {
  return {
    AppName: w?.app_name ?? "",
    Clients: (w?.clients ?? []).map(mapMCPClient),
  };
}

function mapLiveProcess(w: WireLiveProcess): LiveProcessDTO {
  return {
    SessionID: w.session_id ?? "",
    AgentName: w.agent_name ?? "",
    PID: w.pid ?? 0,
    Lifecycle: w.lifecycle ?? "",
    Substate: w.substate ?? "",
  };
}

function mapConfigField(w: WireConfigField): ConfigFieldDTO {
  return {
    Key: w.key ?? "",
    Value: w.value ?? "",
    Type: w.type ?? "",
    Options: w.options ?? "",
    IsSecret: w.is_secret ?? false,
    Description: w.description ?? "",
    Required: w.required ?? false,
  };
}

function mapProviderStatus(w: WireProviderStatus): ProviderStatusDTO {
  return {
    Instance: mapInstance(w.instance),
    Path: w.path ?? "",
    PathFound: w.path_found ?? false,
    Version: w.version ?? "",
    VersionErr: w.version_err ?? "",
    Probing: w.probing ?? false,
    Hooks: mapHooks(w.hooks),
    Cap: mapCap(w.cap),
    HookEnabled: w.hook_enabled ?? {},
  };
}

export function normalizeProviders(r: WireProvidersListResponse): ProvidersListResponse {
  return {
    Providers: (r.providers ?? []).map(mapProviderStatus),
    Gate: mapGate(r.gate),
    Spawns: (r.spawns ?? []).map(mapSpawn),
    MCPClients: mapMCP(r.mcp),
    AutoRescan: r.auto_rescan ?? false,
    PoolActive: r.pool_active ?? 0,
    PoolQueueLen: r.pool_queue_len ?? 0,
    PoolMax: r.pool_max ?? 0,
    LiveProcesses: (r.live_processes ?? []).map(mapLiveProcess),
    SupportedKeys: r.supported_keys ?? [],
  };
}

export async function apiGetProviders(): Promise<ProvidersListResponse> {
  const r = await get<WireProvidersListResponse>(getBase() + "/api/providers");
  return normalizeProviders(r);
}

export async function apiRescanAll(): Promise<void> {
  return post<void>(getBase() + "/providers/rescan");
}

export async function apiRescanOne(type: string, name: string): Promise<void> {
  return post<void>(getBase() + `/providers/rescan/${encodeURIComponent(type)}/${encodeURIComponent(name)}`);
}

export async function apiGateToggle(): Promise<void> {
  return post<void>(getBase() + "/providers/gate/toggle");
}

export async function apiGateModes(fields: { permission_mode: string }): Promise<void> {
  const form = new URLSearchParams();
  form.set("permission_mode", fields.permission_mode);
  const resp = await fetch(getBase() + "/providers/gate/modes", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form.toString(),
    redirect: "manual",
  });
  if (resp.type === "opaqueredirect" || resp.status === 303 || resp.status === 302) {
    return;
  }
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    throw new ApiError(resp.status, text || `HTTP ${resp.status}`);
  }
}

export async function apiAutoRescanToggle(): Promise<void> {
  return post<void>(getBase() + "/providers/auto-rescan/toggle");
}

export async function apiMCPInstall(clientID: string): Promise<void> {
  return post<void>(getBase() + `/providers/mcp/${encodeURIComponent(clientID)}/install`);
}

export async function apiMCPUninstall(clientID: string): Promise<void> {
  return post<void>(getBase() + `/providers/mcp/${encodeURIComponent(clientID)}/uninstall`);
}

export async function apiDeleteProvider(type: string, name: string): Promise<void> {
  return del<void>(getBase() + `/providers/${encodeURIComponent(type)}/${encodeURIComponent(name)}`);
}

export type RenameProviderResult = {
  status: string;
  name: string;
  projects_migrated: number;
};

export async function apiRenameProvider(type: string, name: string, newName: string): Promise<RenameProviderResult> {
  const form = new URLSearchParams();
  form.set("new_name", newName);
  const resp = await fetch(
    getBase() + `/providers/rename/${encodeURIComponent(type)}/${encodeURIComponent(name)}`,
    {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json" },
      body: form.toString(),
    },
  );
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    let msg = text || `HTTP ${resp.status}`;
    try {
      const j = JSON.parse(text) as { error?: string };
      if (j.error) msg = j.error;
    } catch { /* not JSON — keep raw text */ }
    throw new ApiError(resp.status, msg);
  }
  return resp.json() as Promise<RenameProviderResult>;
}

export async function apiCreateProvider(fields: {
  type: string;
  name: string;
  binary?: string;
  extra_args?: string;
  env?: string;
  use_airouter?: boolean;
  airouter_provider?: string;
  airouter_models?: Record<string, string>;
  airouter_api_key?: string;
  airouter_raw_config?: string;
}): Promise<void> {
  const form = new URLSearchParams();
  form.set("type", fields.type);
  form.set("name", fields.name);
  if (fields.binary) {
    form.set("binary", fields.binary);
  }
  if (fields.extra_args) {
    form.set("extra_args", fields.extra_args);
  }
  if (fields.env) {
    form.set("env", fields.env);
  }
  if (fields.use_airouter) {
    form.set("use_airouter", "on");
  }
  if (fields.airouter_provider) {
    form.set("airouter_provider", fields.airouter_provider);
  }
  for (const [slot, model] of Object.entries(fields.airouter_models ?? {})) {
    if (model.trim() !== "") form.set(`airouter_model_${slot}`, model.trim());
  }
  if (fields.airouter_api_key && fields.airouter_api_key.trim() !== "") {
    form.set("airouter_api_key", fields.airouter_api_key);
  }
  if (fields.airouter_raw_config) {
    form.set("airouter_raw_config", fields.airouter_raw_config);
  }
  const resp = await fetch(getBase() + "/providers", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form.toString(),
    redirect: "manual",
  });
  if (resp.type === "opaqueredirect" || resp.status === 303 || resp.status === 302) {
    return;
  }
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    throw new ApiError(resp.status, text || `HTTP ${resp.status}`);
  }
}

export type CatalogValueKind = "bool" | "enum" | "string" | "int";

export type CatalogEntry = {
  key: string;
  description: string;
  kind: CatalogValueKind;
  options?: string[];
  placeholder?: string;
};

export type ProviderCatalog = {
  env: CatalogEntry[];
  args: CatalogEntry[];
};

// apiGetProviderCatalog fetches the curated env + args picker entries for
// a provider type. Type-only — no per-instance state. Returns empty
// arrays for unknown types so the picker just has nothing to offer.
export async function apiGetProviderCatalog(base: string, type: string): Promise<ProviderCatalog> {
  const r = await get<ProviderCatalog | null>(`${base}/providers/catalog/${encodeURIComponent(type)}`);
  return r ?? { env: [], args: [] };
}

export async function apiProbeGate(type: string, name: string): Promise<void> {
  return post<void>(getBase() + `/providers/probe-gate/${encodeURIComponent(type)}/${encodeURIComponent(name)}`);
}

export function normalizeProviderDetail(r: WireProviderDetailResponse): ProviderDetailResponse {
  return {
    Instance: mapInstance(r.instance),
    Path: r.path ?? "",
    PathFound: r.path_found ?? false,
    Version: r.version ?? "",
    VersionErr: r.version_err ?? "",
    Probing: r.probing ?? false,
    Hooks: mapHooks(r.hooks),
    HookEnabled: r.hook_enabled ?? {},
    Gate: mapGate(r.gate),
    GlobalMax: r.global_max ?? 0,
    ActiveCount: r.active_count ?? 0,
    ActivePIDs: (r.active_pids ?? []).map(mapLiveProcess),
    ConfigFields: (r.config_fields ?? []).map(mapConfigField),
    Spawns: (r.spawns ?? []).map(mapSpawn),
    Page: r.page ?? 0,
    HasNext: r.has_next ?? false,
    AIRouter: {
      Supported: r.airouter?.supported ?? false,
      Enabled: r.airouter?.enabled ?? false,
      Provider: r.airouter?.provider ?? "",
      Routers: (r.airouter?.routers ?? []).map((x) => ({ ID: x.id, Name: x.name })),
      Models: r.airouter?.models ?? {},
      KeySet: r.airouter?.key_set ?? false,
      RawConfig: r.airouter?.raw_config ?? "",
      Preview: r.airouter?.preview ?? "",
    },
  };
}

export async function apiGetProviderDetail(base: string, type: string, name: string): Promise<ProviderDetailResponse> {
  const r = await get<WireProviderDetailResponse>(`${base}/api/providers/${encodeURIComponent(type)}/${encodeURIComponent(name)}`);
  return normalizeProviderDetail(r);
}

function mapSpawnEvent(w: WireSpawnEvent): SpawnEvent {
  return {
    Type: w.type ?? "",
    At: w.at ?? "",
    ProviderType: w.provider_type ?? "",
    ProviderName: w.provider_name ?? "",
    AgentName: w.agent_name ?? "",
    Workspace: w.workspace ?? "",
    ResumeID: w.resume_id ?? "",
    Binary: w.binary ?? "",
    Args: w.args ?? [],
    Env: w.env ?? [],
    PID: w.pid ?? 0,
    Origin: w.origin ?? "",
    FirstUserMessage: w.first_user_message ?? "",
    ExitReason: w.exit_reason ?? "",
    DurationMs: w.duration_ms ?? 0,
    Error: w.error ?? "",
    Message: w.message ?? "",
  };
}

// apiGetSpawnDetail fetches one spawn log's metadata, event timeline, and the
// MASKED reproduce variants. apiRevealSpawn fetches the UNMASKED variants (same
// keys), admin-gated — called only when the user picks "Live" env.
export async function apiGetSpawnDetail(base: string, file: string): Promise<SpawnDetailResponse> {
  const r = await get<WireSpawnDetailResponse>(`${base}/api/providers/spawns/${encodeURIComponent(file)}`);
  return {
    File: mapSpawn(r.file),
    Events: (r.events ?? []).map(mapSpawnEvent),
    SessionDeleted: r.session_deleted ?? false,
    Repro: r.repro ?? {},
    HasResume: r.has_resume ?? false,
  };
}

export async function apiRevealSpawn(base: string, file: string): Promise<Record<string, string>> {
  return get<Record<string, string>>(`${base}/providers/spawns/${encodeURIComponent(file)}/reveal`);
}

export async function apiSaveProviderDetail(base: string, type: string, name: string, fields: Record<string, string>): Promise<void> {
  const form = new URLSearchParams(fields);
  const resp = await fetch(
    `${base}/providers/detail/${encodeURIComponent(type)}/${encodeURIComponent(name)}/save`,
    {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: form.toString(),
      redirect: "manual",
    },
  );
  if (resp.type === "opaqueredirect" || resp.status === 303 || resp.status === 302) return;
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    throw new ApiError(resp.status, text || `HTTP ${resp.status}`);
  }
}

export async function apiSaveConfigKey(base: string, type: string, name: string, key: string, value: string): Promise<void> {
  const form = new URLSearchParams({ value });
  const resp = await fetch(
    `${base}/providers/detail/${encodeURIComponent(type)}/${encodeURIComponent(name)}/${encodeURIComponent(key)}`,
    {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json" },
      body: form.toString(),
      redirect: "follow",
    },
  );
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    throw new ApiError(resp.status, text || `HTTP ${resp.status}`);
  }
}

export async function apiHookCheck(base: string, type: string, name: string, event: string): Promise<unknown> {
  return post<unknown>(
    `${base}/providers/${encodeURIComponent(type)}/${encodeURIComponent(name)}/hooks/${encodeURIComponent(event)}/check`,
  );
}

export async function apiHookEnable(base: string, type: string, name: string, event: string): Promise<unknown> {
  return post<unknown>(
    `${base}/providers/${encodeURIComponent(type)}/${encodeURIComponent(name)}/hooks/${encodeURIComponent(event)}/enable`,
  );
}

export async function apiHookDisable(base: string, type: string, name: string, event: string): Promise<unknown> {
  return post<unknown>(
    `${base}/providers/${encodeURIComponent(type)}/${encodeURIComponent(name)}/hooks/${encodeURIComponent(event)}/disable`,
  );
}

export function normalizeStorage(r: StorageResponse): StorageResponse {
  return {
    files: (r.files ?? []).map((f) => ({
      id: f.id ?? 0,
      provider_type: f.provider_type ?? "",
      instance_name: f.instance_name ?? "",
      rel_path: f.rel_path ?? "",
      name: f.name ?? "",
      is_dir: f.is_dir ?? false,
      size: f.size ?? 0,
      synced_at: f.synced_at ?? "",
      retention_days: f.retention_days ?? 0,
    })),
    filter_provider: r.filter_provider ?? "",
    filter_instance: r.filter_instance ?? "",
    provider_types: r.provider_types ?? [],
  };
}

export async function apiGetStorage(filterProvider = "", filterInstance = ""): Promise<StorageResponse> {
  const params = new URLSearchParams();
  if (filterProvider) params.set("provider", filterProvider);
  if (filterInstance) params.set("instance", filterInstance);
  const qs = params.toString();
  const r = await get<StorageResponse>(getBase() + "/api/providers/storage" + (qs ? `?${qs}` : ""));
  return normalizeStorage(r);
}

export async function apiStorageRetention(id: number, days: number): Promise<void> {
  const form = new URLSearchParams({ days: String(days) });
  const resp = await fetch(getBase() + `/providers/storage/${id}/retention`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form.toString(),
  });
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    throw new ApiError(resp.status, text || `HTTP ${resp.status}`);
  }
}

export async function apiStoragePreview(id: number): Promise<Record<string, unknown>> {
  return get<Record<string, unknown>>(getBase() + `/providers/storage/${id}/preview`);
}

export async function apiStorageRestore(ids: number[]): Promise<{ restored: number }> {
  const form = new URLSearchParams();
  for (const id of ids) form.append("ids", String(id));
  const resp = await fetch(getBase() + "/providers/storage/restore", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form.toString(),
  });
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    throw new ApiError(resp.status, text || `HTTP ${resp.status}`);
  }
  return resp.json();
}

export async function apiStorageDelete(id: number): Promise<void> {
  return del<void>(getBase() + `/providers/storage/${id}`);
}

export async function apiStorageSync(type: string, name: string): Promise<void> {
  return post<void>(getBase() + `/providers/storage/sync/${encodeURIComponent(type)}/${encodeURIComponent(name)}`);
}

export async function apiStorageUpload(
  providerType: string,
  instanceName: string,
  relPath: string,
  file: File,
): Promise<void> {
  const form = new FormData();
  form.append("provider_type", providerType);
  form.append("instance_name", instanceName);
  form.append("rel_path", relPath);
  form.append("file", file);
  const resp = await fetch(getBase() + "/providers/storage/upload", {
    method: "POST",
    credentials: "same-origin",
    body: form,
    redirect: "follow",
  });
  if (resp.type === "opaqueredirect" || resp.status === 303 || resp.status === 302) return;
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    throw new ApiError(resp.status, text || `HTTP ${resp.status}`);
  }
}

// ── AI Router ────────────────────────────────────────────────────────

export type AIRouterStatus = {
  installed: boolean;
  running: boolean;
  version: string;
  // "not-installed" | "starting" | "running" | "stopped"
  state: string;
};

// apiAIRouterStatus reports install + run state for the given router so the
// provider form can gate the "Use AI Router" toggle (must be installed +
// running to enable).
export async function apiAIRouterStatus(base: string, id: string): Promise<AIRouterStatus> {
  const r = await get<Partial<AIRouterStatus>>(`${base}/airouter/${encodeURIComponent(id)}/status`);
  return {
    installed: r.installed ?? false,
    running: r.running ?? false,
    version: r.version ?? "",
    state: r.state ?? "stopped",
  };
}

// apiAIRouterStart spawns the given router process (installed required).
// Waits for the dashboard to answer before resolving.
export async function apiAIRouterStart(base: string, id: string): Promise<void> {
  return post<void>(`${base}/airouter/${encodeURIComponent(id)}/start`);
}

export type AIRouterModel = {
  id: string;
  // owned_by groups models in the picker (e.g. "combo", "kc", "gc").
  ownedBy: string;
};

// apiAIRouterModels lists the models the given router serves, via the
// unauthenticated OpenAI-compatible proxy at the wick root
// (/airouter/<id>/v1/models). Returns [] when the endpoint is unreachable
// (process not running) so the caller can show an empty picker + a hint
// instead of throwing.
export async function apiAIRouterModels(id: string): Promise<AIRouterModel[]> {
  try {
    const r = await get<{ data?: Array<{ id?: string; owned_by?: string }> }>(`/airouter/${encodeURIComponent(id)}/v1/models`);
    return (r.data ?? [])
      .map((m) => ({ id: m.id ?? "", ownedBy: m.owned_by ?? "" }))
      .filter((m) => m.id !== "");
  } catch {
    return [];
  }
}

export type AIRouterSlot = {
  key: string;
  label: string;
  placeholder: string;
};

// apiAIRouterSlots returns the model slots a provider type exposes under the
// given router. Defined in the BE so the count/shape differs per router+type.
export async function apiAIRouterSlots(base: string, type: string, routerId: string): Promise<AIRouterSlot[]> {
  const r = await get<{ slots?: AIRouterSlot[] }>(
    `${base}/providers/airouter/slots/${encodeURIComponent(type)}?router=${encodeURIComponent(routerId)}`,
  );
  return r.slots ?? [];
}

// apiSaveAIRouter persists an instance's AI-router settings (toggle +
// selected router + per-slot models + optional key) in one request. Slot
// models are sent as airouter_model_<slot>. A blank key keeps the stored one.
export async function apiSaveAIRouter(
  base: string,
  type: string,
  name: string,
  fields: { use_airouter: boolean; provider: string; models: Record<string, string>; api_key?: string; raw_config?: string },
): Promise<void> {
  const form = new URLSearchParams();
  form.set("use_airouter", fields.use_airouter ? "on" : "false");
  if (fields.provider) {
    form.set("airouter_provider", fields.provider);
  }
  for (const [slot, model] of Object.entries(fields.models)) {
    if (model.trim() !== "") form.set(`airouter_model_${slot}`, model.trim());
  }
  if (fields.api_key && fields.api_key.trim() !== "") {
    form.set("airouter_api_key", fields.api_key);
  }
  // Raw config is free-form (not secret); send it always so clearing persists.
  form.set("airouter_raw_config", fields.raw_config ?? "");
  const resp = await fetch(
    `${base}/providers/detail/${encodeURIComponent(type)}/${encodeURIComponent(name)}/airouter`,
    {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json" },
      body: form.toString(),
      redirect: "follow",
    },
  );
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    let msg = text || `HTTP ${resp.status}`;
    try {
      const j = JSON.parse(text) as { error?: string };
      if (j.error) msg = j.error;
    } catch { /* keep raw */ }
    throw new ApiError(resp.status, msg);
  }
}

export type { StorageFileDTO };

export { ApiError };
