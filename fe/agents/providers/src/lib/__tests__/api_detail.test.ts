import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  apiGetProviderDetail,
  apiSaveConfigKey,
  apiHookCheck,
  apiHookEnable,
  apiHookDisable,
  normalizeProviderDetail,
  ApiError,
} from "../api.js";

beforeEach(() => {
  vi.resetAllMocks();
});

function makeWireDetail() {
  return {
    instance: { type: "claude", name: "default", binary: "claude", disabled: false, max_concurrent: 4, send_mode: "" },
    path: "/usr/bin/claude",
    path_found: true,
    version: "1.2.3",
    version_err: "",
    probing: false,
    hooks: { pre_tool_use: { supported: true, verified: false, probed_at: "", error: "", scope: "" } },
    hook_enabled: { pre_tool_use: false },
    gate: { enabled: false, binary: "", source: "", reason: "", note: "", permission_mode: "", bypass_locked: false },
    global_max: 8,
    active_count: 0,
    active_pids: [],
    config_fields: [
      { key: "api_key", value: "••••••••", type: "text", options: "", is_secret: true, description: "API Key", required: true },
      { key: "model", value: "claude-3", type: "text", options: "", is_secret: false, description: "Model name", required: false },
      { key: "mode", value: "auto", type: "select", options: "auto,manual,off", is_secret: false, description: "Mode", required: false },
    ],
    spawns: [
      { path: "/tmp/s.log", provider_type: "claude", provider_name: "default", session_id: "sess-abc-1234", started_at: "2024-01-01T00:00:00Z", pid: 42, origin: "web", first_user_message: "Hello", binary: "claude", exit_reason: "done" },
    ],
    page: 1,
    has_next: false,
  };
}

describe("normalizeProviderDetail - snake_case wire mapping", () => {
  it("maps snake_case detail fields to PascalCase domain shape", () => {
    const r = normalizeProviderDetail(makeWireDetail());
    expect(r.Instance.Type).toBe("claude");
    expect(r.Instance.MaxConcurrent).toBe(4);
    expect(r.PathFound).toBe(true);
    expect(r.Version).toBe("1.2.3");
    expect(r.GlobalMax).toBe(8);
    expect(r.Hooks["pre_tool_use"].Supported).toBe(true);
    expect(r.HookEnabled["pre_tool_use"]).toBe(false);
  });

  it("maps config_fields snake_case to PascalCase", () => {
    const r = normalizeProviderDetail(makeWireDetail());
    expect(r.ConfigFields.length).toBe(3);
    expect(r.ConfigFields[0].Key).toBe("api_key");
    expect(r.ConfigFields[0].IsSecret).toBe(true);
    expect(r.ConfigFields[1].Value).toBe("claude-3");
  });

});

describe("normalizeProviderDetail - null normalization", () => {
  it("normalizes null hooks to empty object", () => {
    const raw = { ...makeWireDetail(), hooks: null };
    expect(normalizeProviderDetail(raw).Hooks).toEqual({});
  });

  it("normalizes null hook_enabled to empty object", () => {
    const raw = { ...makeWireDetail(), hook_enabled: null };
    expect(normalizeProviderDetail(raw).HookEnabled).toEqual({});
  });

  it("normalizes null active_pids to empty array", () => {
    const raw = { ...makeWireDetail(), active_pids: null };
    expect(normalizeProviderDetail(raw).ActivePIDs).toEqual([]);
  });

  it("normalizes null config_fields to empty array", () => {
    const raw = { ...makeWireDetail(), config_fields: null };
    expect(normalizeProviderDetail(raw).ConfigFields).toEqual([]);
  });

  it("normalizes null gate to default structure", () => {
    const raw = { ...makeWireDetail(), gate: null };
    const r = normalizeProviderDetail(raw);
    expect(r.Gate.Enabled).toBe(false);
    expect(r.Gate.Binary).toBe("");
  });
});

describe("apiGetProviderDetail", () => {
  it("fetches and maps /api/providers/{type}/{name} from snake_case wire", async () => {
    const payload = makeWireDetail();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => payload,
    }));
    const r = await apiGetProviderDetail("", "claude", "default");
    expect(r.Version).toBe("1.2.3");
    expect(r.ConfigFields.length).toBe(3);
    expect(r.ConfigFields[0].Key).toBe("api_key");
    const url = (vi.mocked(fetch) as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toContain("/api/providers/claude/default");
  });

  it("normalizes null arrays from the API response", async () => {
    const payload = { ...makeWireDetail(), active_pids: null, spawns: null, config_fields: null };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => payload,
    }));
    const r = await apiGetProviderDetail("", "claude", "default");
    expect(r.ActivePIDs).toEqual([]);
    expect(r.ConfigFields).toEqual([]);
  });

  it("throws ApiError on non-ok response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false,
      status: 404,
      text: async () => "not found",
    }));
    await expect(apiGetProviderDetail("", "claude", "missing")).rejects.toBeInstanceOf(ApiError);
  });
});

describe("apiSaveConfigKey", () => {
  it("POSTs to the correct per-key URL", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: { get: () => "application/json" },
      json: async () => ({ ok: true }),
    }));
    await apiSaveConfigKey("", "claude", "default", "api_key", "new-secret");
    const url = (vi.mocked(fetch) as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toContain("/providers/detail/claude/default/api_key");
  });
});

describe("apiHookCheck / Enable / Disable", () => {
  it("apiHookCheck POSTs to hook check URL", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: { get: () => "application/json" },
      json: async () => ({}),
    }));
    await apiHookCheck("", "claude", "default", "pre_tool_use");
    const url = (vi.mocked(fetch) as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toContain("/hooks/pre_tool_use/check");
  });

  it("apiHookEnable POSTs to hook enable URL", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: { get: () => "application/json" },
      json: async () => ({}),
    }));
    await apiHookEnable("", "claude", "default", "pre_tool_use");
    const url = (vi.mocked(fetch) as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toContain("/hooks/pre_tool_use/enable");
  });

  it("apiHookDisable POSTs to hook disable URL", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: { get: () => "application/json" },
      json: async () => ({}),
    }));
    await apiHookDisable("", "claude", "default", "pre_tool_use");
    const url = (vi.mocked(fetch) as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toContain("/hooks/pre_tool_use/disable");
  });
});
