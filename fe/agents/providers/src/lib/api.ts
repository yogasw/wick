import type { ProvidersListResponse } from "./types.js";

class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message);
  }
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

export function normalizeProviders(r: ProvidersListResponse): ProvidersListResponse {
  return {
    ...r,
    Providers: (r.Providers ?? []).map((p) => ({
      ...p,
      Hooks: p.Hooks ?? {},
      HookEnabled: p.HookEnabled ?? {},
    })),
    Spawns: r.Spawns ?? [],
    LiveProcesses: r.LiveProcesses ?? [],
    SupportedKeys: r.SupportedKeys ?? [],
    MCPClients: {
      AppName: r.MCPClients?.AppName ?? "",
      Clients: r.MCPClients?.Clients ?? [],
    },
    Gate: r.Gate ?? {
      Enabled: false,
      Binary: "",
      Source: "",
      Reason: "",
      Note: "",
      PermissionMode: "",
      BypassLocked: false,
    },
  };
}

export async function apiGetProviders(): Promise<ProvidersListResponse> {
  const r = await get<ProvidersListResponse>("/api/providers");
  return normalizeProviders(r);
}

export async function apiRescanAll(): Promise<void> {
  return post<void>("/providers/rescan");
}

export async function apiRescanOne(type: string, name: string): Promise<void> {
  return post<void>(`/providers/rescan/${encodeURIComponent(type)}/${encodeURIComponent(name)}`);
}

export async function apiGateToggle(): Promise<void> {
  return post<void>("/providers/gate/toggle");
}

export async function apiGateModes(modes: Record<string, boolean>): Promise<void> {
  return post<void>("/providers/gate/modes", modes);
}

export async function apiAutoRescanToggle(): Promise<void> {
  return post<void>("/providers/auto-rescan/toggle");
}

export async function apiMCPInstall(clientID: string): Promise<void> {
  return post<void>(`/providers/mcp/${encodeURIComponent(clientID)}/install`);
}

export async function apiMCPUninstall(clientID: string): Promise<void> {
  return post<void>(`/providers/mcp/${encodeURIComponent(clientID)}/uninstall`);
}

export async function apiDeleteProvider(type: string, name: string): Promise<void> {
  return del<void>(`/providers/${encodeURIComponent(type)}/${encodeURIComponent(name)}`);
}

export async function apiProbeGate(type: string, name: string): Promise<void> {
  return post<void>(`/providers/probe-gate/${encodeURIComponent(type)}/${encodeURIComponent(name)}`);
}

export { ApiError };
