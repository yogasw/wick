import { describe, it, expect, vi, beforeEach } from "vitest";
import { apiGetProviders, normalizeProviders } from "../api.js";
import type { ProvidersListResponse } from "../types.js";

beforeEach(() => {
  vi.resetAllMocks();
});

function makeMinimal(): ProvidersListResponse {
  return {
    Providers: [],
    Gate: { Enabled: false, Binary: "", Source: "", Reason: "", Note: "", PermissionMode: "", BypassLocked: false },
    Spawns: [],
    MCPClients: { AppName: "", Clients: [] },
    AutoRescan: false,
    PoolActive: 0,
    PoolQueueLen: 0,
    PoolMax: 0,
    LiveProcesses: [],
    SupportedKeys: [],
  };
}

describe("normalizeProviders - null normalization", () => {
  it("normalizes null Providers to empty array", () => {
    const raw = { ...makeMinimal(), Providers: null } as unknown as ProvidersListResponse;
    const r = normalizeProviders(raw);
    expect(r.Providers).toEqual([]);
  });

  it("normalizes null Spawns to empty array", () => {
    const raw = { ...makeMinimal(), Spawns: null } as unknown as ProvidersListResponse;
    const r = normalizeProviders(raw);
    expect(r.Spawns).toEqual([]);
  });

  it("normalizes null LiveProcesses to empty array", () => {
    const raw = { ...makeMinimal(), LiveProcesses: null } as unknown as ProvidersListResponse;
    const r = normalizeProviders(raw);
    expect(r.LiveProcesses).toEqual([]);
  });

  it("normalizes null SupportedKeys to empty array", () => {
    const raw = { ...makeMinimal(), SupportedKeys: null } as unknown as ProvidersListResponse;
    const r = normalizeProviders(raw);
    expect(r.SupportedKeys).toEqual([]);
  });

  it("normalizes null MCPClients to empty structure", () => {
    const raw = { ...makeMinimal(), MCPClients: null } as unknown as ProvidersListResponse;
    const r = normalizeProviders(raw);
    expect(r.MCPClients.Clients).toEqual([]);
    expect(r.MCPClients.AppName).toBe("");
  });

  it("normalizes null Hooks/HookEnabled on providers", () => {
    const raw: ProvidersListResponse = {
      ...makeMinimal(),
      Providers: [{
        Instance: { Type: "claude", Name: "claude", Binary: "", Disabled: false, MaxConcurrent: 1, SendMode: "" },
        Path: "/usr/bin/claude",
        PathFound: true,
        Version: "1.0.0",
        VersionErr: "",
        Probing: false,
        Hooks: null as unknown as Record<string, never>,
        HookEnabled: null as unknown as Record<string, boolean>,
        Cap: { Used: 0, Max: 1, Unlimited: false },
      }],
    };
    const r = normalizeProviders(raw);
    expect(r.Providers[0].Hooks).toEqual({});
    expect(r.Providers[0].HookEnabled).toEqual({});
  });

  it("passes through valid data unchanged", () => {
    const payload = makeMinimal();
    payload.AutoRescan = true;
    payload.PoolMax = 4;
    const r = normalizeProviders(payload);
    expect(r.AutoRescan).toBe(true);
    expect(r.PoolMax).toBe(4);
  });
});

describe("apiGetProviders", () => {
  it("fetches and normalizes /api/providers", async () => {
    const payload = makeMinimal();
    payload.PoolActive = 2;
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => payload,
    }));
    const r = await apiGetProviders();
    expect(r.PoolActive).toBe(2);
    expect(r.Providers).toEqual([]);
  });

  it("throws ApiError on non-ok response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false,
      status: 503,
      text: async () => "service unavailable",
    }));
    await expect(apiGetProviders()).rejects.toThrow("service unavailable");
  });
});
