import { describe, it, expect, vi, beforeEach } from "vitest";
import { apiGetProviders, normalizeProviders } from "../api.js";

beforeEach(() => {
  vi.resetAllMocks();
});

function makeWire() {
  return {
    providers: [
      {
        instance: { type: "claude", name: "claude", binary: "claude", disabled: false, max_concurrent: 4, send_mode: "" },
        path: "/usr/bin/claude",
        path_found: true,
        version: "1.2.3",
        version_err: "",
        probing: false,
        hooks: { pre_tool_use: { supported: true, verified: true, probed_at: "2024-01-01T00:00:00Z", error: "", scope: "global" } },
        cap: { used: 1, max: 4, unlimited: false },
        hook_enabled: { pre_tool_use: false },
      },
    ],
    gate: { enabled: true, binary: "/usr/bin/gate", source: "config", reason: "", note: "Gate note", permission_mode: "bypass", bypass_locked: false },
    spawns: [
      { path: "/tmp/s.log", provider_type: "claude", provider_name: "claude", session_id: "sess-1", started_at: "2024-01-01T00:00:00Z", pid: 42, origin: "web", first_user_message: "hi", binary: "claude", exit_reason: "done" },
    ],
    mcp: {
      app_name: "wick-agent",
      clients: [
        { id: "mcp-1", label: "Wick MCP", detected: true, installed: false, blocklisted: false, config_path: "/home/x/.config" },
      ],
    },
    auto_rescan: false,
    pool_active: 0,
    pool_queue_len: 0,
    pool_max: 2,
    live_processes: [
      { session_id: "live-1", agent_name: "claude", pid: 99, lifecycle: "running", substate: "active" },
    ],
    supported_keys: ["claude", "openai"],
  };
}

describe("normalizeProviders - snake_case wire mapping", () => {
  it("maps snake_case provider fields to PascalCase domain shape", () => {
    const r = normalizeProviders(makeWire());
    const p = r.Providers[0];
    expect(p.Instance.Type).toBe("claude");
    expect(p.Instance.MaxConcurrent).toBe(4);
    expect(p.Instance.SendMode).toBe("");
    expect(p.Path).toBe("/usr/bin/claude");
    expect(p.PathFound).toBe(true);
    expect(p.Version).toBe("1.2.3");
    expect(p.Cap.Used).toBe(1);
    expect(p.Cap.Max).toBe(4);
    expect(p.HookEnabled["pre_tool_use"]).toBe(false);
    expect(p.Hooks["pre_tool_use"].Supported).toBe(true);
    expect(p.Hooks["pre_tool_use"].ProbedAt).toBe("2024-01-01T00:00:00Z");
  });

  it("maps gate snake_case fields to PascalCase", () => {
    const r = normalizeProviders(makeWire());
    expect(r.Gate.Enabled).toBe(true);
    expect(r.Gate.PermissionMode).toBe("bypass");
    expect(r.Gate.BypassLocked).toBe(false);
    expect(r.Gate.Note).toBe("Gate note");
  });

  it("maps mcp -> MCPClients with PascalCase client fields", () => {
    const r = normalizeProviders(makeWire());
    expect(r.MCPClients.AppName).toBe("wick-agent");
    expect(r.MCPClients.Clients[0].ID).toBe("mcp-1");
    expect(r.MCPClients.Clients[0].ConfigPath).toBe("/home/x/.config");
  });

  it("maps live_processes and supported_keys", () => {
    const r = normalizeProviders(makeWire());
    expect(r.LiveProcesses[0].SessionID).toBe("live-1");
    expect(r.LiveProcesses[0].PID).toBe(99);
    expect(r.SupportedKeys).toEqual(["claude", "openai"]);
  });

  it("maps spawns snake_case to PascalCase", () => {
    const r = normalizeProviders(makeWire());
    expect(r.Spawns[0].SessionID).toBe("sess-1");
    expect(r.Spawns[0].ProviderType).toBe("claude");
    expect(r.Spawns[0].PID).toBe(42);
  });

  it("maps pool stats", () => {
    const r = normalizeProviders(makeWire());
    expect(r.PoolMax).toBe(2);
    expect(r.AutoRescan).toBe(false);
  });
});

describe("normalizeProviders - null normalization", () => {
  function makeNullWire() {
    return {
      providers: null,
      gate: null,
      spawns: null,
      mcp: null,
      auto_rescan: false,
      pool_active: 0,
      pool_queue_len: 0,
      pool_max: 0,
      live_processes: null,
      supported_keys: null,
    };
  }

  it("normalizes null providers to empty array", () => {
    expect(normalizeProviders(makeNullWire()).Providers).toEqual([]);
  });

  it("normalizes null spawns to empty array", () => {
    expect(normalizeProviders(makeNullWire()).Spawns).toEqual([]);
  });

  it("normalizes null live_processes to empty array", () => {
    expect(normalizeProviders(makeNullWire()).LiveProcesses).toEqual([]);
  });

  it("normalizes null supported_keys to empty array", () => {
    expect(normalizeProviders(makeNullWire()).SupportedKeys).toEqual([]);
  });

  it("normalizes null mcp to empty structure", () => {
    const r = normalizeProviders(makeNullWire());
    expect(r.MCPClients.Clients).toEqual([]);
    expect(r.MCPClients.AppName).toBe("");
  });

  it("normalizes null gate to default structure", () => {
    const r = normalizeProviders(makeNullWire());
    expect(r.Gate.Enabled).toBe(false);
    expect(r.Gate.Binary).toBe("");
  });

  it("normalizes null hooks/hook_enabled on providers", () => {
    const raw = {
      ...makeNullWire(),
      providers: [{
        instance: { type: "claude", name: "claude", binary: "", disabled: false, max_concurrent: 1, send_mode: "" },
        path: "/usr/bin/claude",
        path_found: true,
        version: "1.0.0",
        version_err: "",
        probing: false,
        hooks: null,
        cap: { used: 0, max: 1, unlimited: false },
        hook_enabled: null,
      }],
    };
    const r = normalizeProviders(raw);
    expect(r.Providers[0].Hooks).toEqual({});
    expect(r.Providers[0].HookEnabled).toEqual({});
  });
});

describe("apiGetProviders", () => {
  it("fetches and maps /api/providers from snake_case wire payload", async () => {
    const payload = { ...makeWire(), pool_active: 2 };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => payload,
    }));
    const r = await apiGetProviders();
    expect(r.PoolActive).toBe(2);
    expect(r.Providers[0].Instance.Type).toBe("claude");
    expect(r.Gate.Enabled).toBe(true);
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
