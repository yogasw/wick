import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  apiGetWickConfig,
  apiSaveWickModel,
  apiDeleteWickModel,
  apiSetWickModelDefault,
  apiSaveWickSettings,
  apiDiscoverWickModels,
  apiGetWickInteractions,
  ApiError,
} from "../api.js";

beforeEach(() => {
  vi.resetAllMocks();
});

function okJson(body: unknown) {
  return {
    ok: true,
    status: 200,
    type: "default",
    headers: { get: () => "application/json" },
    json: async () => body,
    text: async () => JSON.stringify(body),
  };
}

function makeWireConfig() {
  return {
    models: [
      {
        id: "m_1",
        kind: "google",
        label: "Gemini Flash",
        model: "gemini-flash-latest",
        key_masked: "••••••••3kQ",
        has_key: true,
        base_url: "",
        api_format: "gemini",
        max_output_tokens: 0,
        default: true,
        temperature: null,
        top_p: null,
        thinking_budget: null,
        raw_config: "",
      },
      {
        id: "m_2",
        kind: "openrouter",
        label: "",
        model: "x-ai/grok-4.5",
        key_masked: "••••••••aB2",
        has_key: true,
        base_url: "https://openrouter.ai/api/v1",
        api_format: "openai_chat",
        max_output_tokens: 8192,
        default: false,
        temperature: 0.7,
        top_p: 0.9,
        thinking_budget: 0,
        raw_config: "{}",
      },
    ],
    settings: {
      shell_tool_disabled: false,
      connectors: ["c1", "c2"],
      max_context_tokens: 128000,
      max_turns: 12,
      temperature: 0.5,
      top_p: null,
      thinking_budget: 2048,
      raw_config: "{\"x\":1}",
    },
  };
}

describe("apiGetWickConfig", () => {
  it("maps snake_case models + settings to PascalCase DTOs", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(okJson(makeWireConfig())));
    const cfg = await apiGetWickConfig("/wick");
    expect(cfg.models).toHaveLength(2);
    expect(cfg.models[0].ID).toBe("m_1");
    expect(cfg.models[0].Kind).toBe("google");
    expect(cfg.models[0].KeyMasked).toBe("••••••••3kQ");
    expect(cfg.models[0].HasKey).toBe(true);
    expect(cfg.models[0].Default).toBe(true);
    expect(cfg.models[0].Temperature).toBeNull();
    expect(cfg.models[1].MaxOutputTokens).toBe(8192);
    expect(cfg.models[1].Temperature).toBe(0.7);
    expect(cfg.settings.ShellToolDisabled).toBe(false);
    expect(cfg.settings.Connectors).toEqual(["c1", "c2"]);
    expect(cfg.settings.MaxContextTokens).toBe(128000);
    expect(cfg.settings.ThinkingBudget).toBe(2048);
    expect(cfg.settings.RawConfig).toBe("{\"x\":1}");
    const url = (vi.mocked(fetch) as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toBe("/wick/providers/wick/config");
  });

  it("normalizes null models/settings to safe defaults", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(okJson({ models: null, settings: null })));
    const cfg = await apiGetWickConfig("");
    expect(cfg.models).toEqual([]);
    expect(cfg.settings.Connectors).toEqual([]);
    expect(cfg.settings.ShellToolDisabled).toBe(false);
    // stream_disabled defaults false on the wire → streaming ON in the FE.
    expect(cfg.settings.EnableStreaming).toBe(true);
    expect(cfg.settings.Temperature).toBeNull();
  });

  it("inverts stream_disabled into EnableStreaming", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(okJson({ models: [], settings: { stream_disabled: true } })));
    const cfg = await apiGetWickConfig("");
    expect(cfg.settings.EnableStreaming).toBe(false);
  });

  it("throws ApiError on non-ok response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 500, text: async () => "boom" }));
    await expect(apiGetWickConfig("")).rejects.toBeInstanceOf(ApiError);
  });
});

describe("apiSaveWickModel", () => {
  it("POSTs a JSON body and returns {status,id}", async () => {
    const fetchMock = vi.fn().mockResolvedValue(okJson({ status: "ok", id: "m_new" }));
    vi.stubGlobal("fetch", fetchMock);
    const res = await apiSaveWickModel("/wick", {
      kind: "openai",
      model: "gpt-5.2",
      label: "GPT 5.2",
      api_key: "sk-secret",
      max_output_tokens: 16384,
      temperature: 0.4,
    });
    expect(res).toEqual({ status: "ok", id: "m_new" });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/wick/providers/wick/models");
    expect(init.method).toBe("POST");
    const body = JSON.parse(init.body as string);
    expect(body.kind).toBe("openai");
    expect(body.model).toBe("gpt-5.2");
    expect(body.api_key).toBe("sk-secret");
    expect(body.max_output_tokens).toBe(16384);
  });

  it("omits api_key when not provided (keep stored key on edit)", async () => {
    const fetchMock = vi.fn().mockResolvedValue(okJson({ status: "ok", id: "m_1" }));
    vi.stubGlobal("fetch", fetchMock);
    await apiSaveWickModel("", { id: "m_1", kind: "google", model: "gemini-flash-latest" });
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    const body = JSON.parse(init.body as string);
    expect(body.id).toBe("m_1");
    expect("api_key" in body).toBe(false);
  });
});

describe("apiDeleteWickModel", () => {
  it("DELETEs the per-id URL", async () => {
    const fetchMock = vi.fn().mockResolvedValue(okJson({ status: "ok" }));
    vi.stubGlobal("fetch", fetchMock);
    await apiDeleteWickModel("/wick", "m_9");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/wick/providers/wick/models/m_9");
    expect(init.method).toBe("DELETE");
  });
});

describe("apiSetWickModelDefault", () => {
  it("POSTs to the default URL", async () => {
    const fetchMock = vi.fn().mockResolvedValue(okJson({ status: "ok" }));
    vi.stubGlobal("fetch", fetchMock);
    await apiSetWickModelDefault("/wick", "m_2");
    const url = fetchMock.mock.calls[0][0] as string;
    expect(url).toBe("/wick/providers/wick/models/m_2/default");
  });
});

describe("apiSaveWickSettings", () => {
  it("POSTs the settings JSON body", async () => {
    const fetchMock = vi.fn().mockResolvedValue(okJson({ status: "ok" }));
    vi.stubGlobal("fetch", fetchMock);
    await apiSaveWickSettings("/wick", {
      shell_tool_disabled: true,
      max_context_tokens: 64000,
      max_turns: 8,
      raw_config: "{}",
    });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/wick/providers/wick/settings");
    expect(init.method).toBe("POST");
    const body = JSON.parse(init.body as string);
    expect(body.shell_tool_disabled).toBe(true);
    expect(body.max_context_tokens).toBe(64000);
    expect(body.raw_config).toBe("{}");
  });
});

describe("apiDiscoverWickModels", () => {
  it("maps the models list and drops blank ids", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      okJson({ models: [{ id: "gpt-5.2", label: "GPT 5.2" }, { id: "", label: "bad" }, { id: "o4-mini" }] }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const r = await apiDiscoverWickModels("/wick", { kind: "openai", api_key: "sk-x" });
    expect(r.error).toBe("");
    expect(r.models).toEqual([
      { id: "gpt-5.2", label: "GPT 5.2" },
      { id: "o4-mini", label: "o4-mini" },
    ]);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/wick/providers/wick/models/discover");
    const body = JSON.parse(init.body as string);
    expect(body.kind).toBe("openai");
    expect(body.api_key).toBe("sk-x");
  });

  it("surfaces the error field for the free-text fallback", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(okJson({ models: [], error: "invalid key" })));
    const r = await apiDiscoverWickModels("", { kind: "anthropic", model_ref: "m_1" });
    expect(r.error).toBe("invalid key");
    expect(r.models).toEqual([]);
  });
});

describe("apiGetWickInteractions", () => {
  it("returns the interaction records and hits the session-scoped URL", async () => {
    const recs = [
      { seq: 1, kind: "generate", model: "gpt-5.2", latency_ms: 320, request: [{ role: "user", text: "hi" }], response: "hello", prompt_tokens: 10, output_tokens: 3 },
      { seq: 2, kind: "generate", model: "gpt-5.2", latency_ms: 90, request: [], error: "429 rate limited" },
    ];
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(okJson({ interactions: recs, total: 2, page: 1, page_size: 10 })));
    const out = await apiGetWickInteractions("/wick", "sess-123");
    expect(out.interactions).toHaveLength(2);
    expect(out.total).toBe(2);
    expect(out.interactions[0].response).toBe("hello");
    expect(out.interactions[1].error).toBe("429 rate limited");
    const url = (vi.mocked(fetch) as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toBe("/wick/providers/wick/interactions/sess-123");
  });

  it("passes search / sort / pagination as query params", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(okJson({ interactions: [], total: 0, page: 2, page_size: 5 })));
    await apiGetWickInteractions("/wick", "s", { q: "shell", sort: "oldest", page: 2, pageSize: 5 });
    const url = (vi.mocked(fetch) as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toContain("q=shell");
    expect(url).toContain("sort=oldest");
    expect(url).toContain("page=2");
    expect(url).toContain("page_size=5");
  });

  it("normalizes a null/absent list to an empty array", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(okJson({ interactions: null })));
    expect((await apiGetWickInteractions("", "s")).interactions).toEqual([]);
  });
});
