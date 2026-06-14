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
import type { ProviderDetailResponse } from "../types.js";

beforeEach(() => {
  vi.resetAllMocks();
});

function makeDetail(): ProviderDetailResponse {
  return {
    Instance: { Type: "claude", Name: "default", Binary: "claude", Disabled: false, MaxConcurrent: 4, SendMode: "" },
    Path: "/usr/bin/claude",
    PathFound: true,
    Version: "1.2.3",
    VersionErr: "",
    Probing: false,
    Hooks: { pre_tool_use: { Supported: true, Verified: false, ProbedAt: "", Error: "", Scope: "" } },
    HookEnabled: { pre_tool_use: false },
    Gate: { Enabled: false, Binary: "", Source: "", Reason: "", Note: "", PermissionMode: "", BypassLocked: false },
    GlobalMax: 8,
    ActiveCount: 0,
    ActivePIDs: [],
    ConfigFields: [
      { Key: "api_key", Value: "••••••••", Type: "text", Options: "", IsSecret: true, Description: "API Key", Required: true },
      { Key: "model", Value: "claude-3", Type: "text", Options: "", IsSecret: false, Description: "Model name", Required: false },
      { Key: "mode", Value: "auto", Type: "select", Options: "auto,manual,off", IsSecret: false, Description: "Mode", Required: false },
    ],
    Spawns: [],
    Page: 1,
    HasNext: false,
  };
}

describe("normalizeProviderDetail - null normalization", () => {
  it("normalizes null Hooks to empty object", () => {
    const raw = { ...makeDetail(), Hooks: null } as unknown as ProviderDetailResponse;
    expect(normalizeProviderDetail(raw).Hooks).toEqual({});
  });

  it("normalizes null HookEnabled to empty object", () => {
    const raw = { ...makeDetail(), HookEnabled: null } as unknown as ProviderDetailResponse;
    expect(normalizeProviderDetail(raw).HookEnabled).toEqual({});
  });

  it("normalizes null ActivePIDs to empty array", () => {
    const raw = { ...makeDetail(), ActivePIDs: null } as unknown as ProviderDetailResponse;
    expect(normalizeProviderDetail(raw).ActivePIDs).toEqual([]);
  });

  it("normalizes null ConfigFields to empty array", () => {
    const raw = { ...makeDetail(), ConfigFields: null } as unknown as ProviderDetailResponse;
    expect(normalizeProviderDetail(raw).ConfigFields).toEqual([]);
  });

  it("normalizes null Spawns to empty array", () => {
    const raw = { ...makeDetail(), Spawns: null } as unknown as ProviderDetailResponse;
    expect(normalizeProviderDetail(raw).Spawns).toEqual([]);
  });

  it("passes through valid data unchanged", () => {
    const d = makeDetail();
    const r = normalizeProviderDetail(d);
    expect(r.Version).toBe("1.2.3");
    expect(r.ConfigFields.length).toBe(3);
    expect(r.Hooks["pre_tool_use"].Supported).toBe(true);
  });
});

describe("apiGetProviderDetail", () => {
  it("fetches and normalizes /api/providers/{type}/{name}", async () => {
    const payload = makeDetail();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => payload,
    }));
    const r = await apiGetProviderDetail("", "claude", "default");
    expect(r.Version).toBe("1.2.3");
    expect(r.ConfigFields.length).toBe(3);
    const url = (vi.mocked(fetch) as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toContain("/api/providers/claude/default");
  });

  it("normalizes null arrays from the API response", async () => {
    const payload = { ...makeDetail(), ActivePIDs: null, Spawns: null, ConfigFields: null };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => payload,
    }));
    const r = await apiGetProviderDetail("", "claude", "default");
    expect(r.ActivePIDs).toEqual([]);
    expect(r.Spawns).toEqual([]);
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
