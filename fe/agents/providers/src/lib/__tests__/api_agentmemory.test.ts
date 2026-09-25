import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  apiAgentMemoryTest,
  apiSaveAgentMemory,
  normalizeProviderDetail,
  ApiError,
} from "../api.js";

beforeEach(() => {
  vi.resetAllMocks();
});

/* A wire payload with nothing but the fields normalizeProviderDetail needs to
   not throw. agent_memory is added per test so the absent case is covered. */
function bareWire(): Record<string, unknown> {
  return {
    instance: { type: "claude", name: "default", binary: "claude", disabled: false, max_concurrent: 4, send_mode: "" },
    path: "/usr/bin/claude",
    path_found: true,
    version: "1.2.3",
    probing: false,
    hooks: null,
    hook_enabled: null,
    gate: null,
    global_max: 8,
    active_count: 0,
    active_pids: null,
    config_fields: null,
  };
}

describe("normalizeProviderDetail - agent_memory", () => {
  it("maps the backend list and the settings", () => {
    const r = normalizeProviderDetail({
      ...bareWire(),
      agent_memory: {
        feature_enabled: true,
        supported: true,
        capture_supported: false,
        capture_note: "Recording is not wired for codex right now",
        enabled: true,
        provider: "ai-memory",
        backends: [{ id: "ai-memory", name: "ai-memory", blurb: "local memory server", github_url: "https://github.com/x/ai-memory" }],
        server_url: "",
        effective_url: "http://127.0.0.1:49374",
        key_set: true,
        capture: false,
        preview: "AI_MEMORY_SERVER_URL=http://127.0.0.1:49374",
      },
    } as never);
    expect(r.AgentMemory.Supported).toBe(true);
    expect(r.AgentMemory.CaptureSupported).toBe(false);
    expect(r.AgentMemory.CaptureNote).toContain("codex");
    expect(r.AgentMemory.EffectiveURL).toBe("http://127.0.0.1:49374");
    expect(r.AgentMemory.Backends).toEqual([
      { ID: "ai-memory", Name: "ai-memory", Blurb: "local memory server", GitHubURL: "https://github.com/x/ai-memory" },
    ]);
    // KeySet is the ONLY thing said about the stored token.
    expect(r.AgentMemory.KeySet).toBe(true);
    expect(Object.keys(r.AgentMemory)).not.toContain("AuthKey");
  });

  it("defaults an absent agent_memory block to off and unsupported", () => {
    // A server that predates the feature must not grow a card whose save
    // would 404 — every default here is the one that hides the control.
    const r = normalizeProviderDetail(bareWire() as never);
    expect(r.AgentMemory.FeatureEnabled).toBe(false);
    expect(r.AgentMemory.Supported).toBe(false);
    expect(r.AgentMemory.CaptureSupported).toBe(false);
    expect(r.AgentMemory.Backends).toEqual([]);
  });
});

describe("apiSaveAgentMemory", () => {
  function stubOK() {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, status: 200, headers: { get: () => "" }, text: async () => "" }));
  }
  function sentBody(): URLSearchParams {
    const init = (vi.mocked(fetch) as ReturnType<typeof vi.fn>).mock.calls[0][1] as RequestInit;
    return new URLSearchParams(init.body as string);
  }

  it("posts to the instance's agentmemory route", async () => {
    stubOK();
    await apiSaveAgentMemory("/b", "claude", "default", {
      use_agent_memory: true, provider: "ai-memory", server_url: "", capture: true,
    });
    const url = (vi.mocked(fetch) as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toBe("/b/providers/detail/claude/default/agentmemory");
  });

  it("always sends server_url so clearing it persists", async () => {
    // Empty is a REAL choice ("use the daemon wick manages"), not an absent
    // field — omitting it would make an instance unpointable back at the
    // managed daemon once it had been sent elsewhere.
    stubOK();
    await apiSaveAgentMemory("", "claude", "default", {
      use_agent_memory: true, provider: "ai-memory", server_url: "  ", capture: false,
    });
    const body = sentBody();
    expect(body.has("agent_memory_server_url")).toBe(true);
    expect(body.get("agent_memory_server_url")).toBe("");
    expect(body.get("agent_memory_capture")).toBe("false");
  });

  it("omits an empty token so the stored one survives", async () => {
    stubOK();
    await apiSaveAgentMemory("", "claude", "default", {
      use_agent_memory: true, provider: "ai-memory", server_url: "", capture: false, auth_key: "",
    });
    expect(sentBody().has("agent_memory_auth_key")).toBe(false);
  });

  it("sends a token the user actually typed", async () => {
    stubOK();
    await apiSaveAgentMemory("", "claude", "default", {
      use_agent_memory: true, provider: "ai-memory", server_url: "", capture: false, auth_key: "s3cret",
    });
    expect(sentBody().get("agent_memory_auth_key")).toBe("s3cret");
  });

  it("surfaces the BE's error message", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false, status: 500, text: async () => JSON.stringify({ error: "disk full" }),
    }));
    await expect(
      apiSaveAgentMemory("", "claude", "default", { use_agent_memory: true, provider: "ai-memory", server_url: "", capture: false }),
    ).rejects.toMatchObject({ message: "disk full" });
  });

  it("throws ApiError on a refusal with no JSON body", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 403, text: async () => "" }));
    await expect(
      apiSaveAgentMemory("", "claude", "default", { use_agent_memory: true, provider: "ai-memory", server_url: "", capture: false }),
    ).rejects.toBeInstanceOf(ApiError);
  });
});

describe("apiAgentMemoryTest", () => {
  it("maps a healthy probe", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true, status: 200,
      headers: { get: () => "application/json" },
      json: async () => ({ ok: true, base_url: "http://127.0.0.1:49374", version: "2.4.0" }),
    }));
    const p = await apiAgentMemoryTest("/b", "ai-memory");
    expect(p).toEqual({ ok: true, baseUrl: "http://127.0.0.1:49374", error: "", version: "2.4.0" });
  });

  it("reports a transport failure as a not-ok probe instead of throwing", async () => {
    // The caller's only job is to say "do not enable this yet", and an
    // unreachable wick endpoint means that just as much as an unreachable
    // daemon does — a thrown error would instead surface as a save failure.
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));
    const p = await apiAgentMemoryTest("", "ai-memory");
    expect(p.ok).toBe(false);
    expect(p.error).toBe("network down");
  });

  it("reports a daemon that answered but is unhealthy", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true, status: 200,
      headers: { get: () => "application/json" },
      json: async () => ({ ok: false, base_url: "http://127.0.0.1:49374", error: "no 200 from http://127.0.0.1:49374/healthz" }),
    }));
    const p = await apiAgentMemoryTest("", "ai-memory");
    expect(p.ok).toBe(false);
    expect(p.error).toContain("healthz");
  });
});
