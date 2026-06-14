import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

vi.mock("$lib/api.js", async () => {
  const actual = await vi.importActual<typeof import("$lib/api.js")>("$lib/api.js");
  return actual;
});

const mockFetch = vi.fn();
vi.stubGlobal("fetch", mockFetch);

function mockAppBase(base: string) {
  const el = document.createElement("div");
  el.id = "app";
  el.dataset.base = base;
  document.body.appendChild(el);
  return () => document.body.removeChild(el);
}

describe("listPresets", () => {
  let cleanup: () => void;

  beforeEach(() => {
    cleanup = mockAppBase("http://localhost:9425/tools/agents");
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("normalizes null presets to empty array", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ presets: null }),
    });
    const { listPresets } = await import("$lib/api.js");
    const result = await listPresets();
    expect(result.presets).toEqual([]);
  });

  it("returns preset list", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ presets: [{ name: "default", is_default: true }, { name: "reviewer" }] }),
    });
    const { listPresets } = await import("$lib/api.js");
    const result = await listPresets();
    expect(result.presets).toHaveLength(2);
    expect(result.presets[0].name).toBe("default");
  });
});

describe("getPreset", () => {
  let cleanup: () => void;

  beforeEach(() => {
    cleanup = mockAppBase("http://localhost:9425/tools/agents");
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("returns preset detail", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ name: "reviewer", body: "You are a reviewer" }),
    });
    const { getPreset } = await import("$lib/api.js");
    const result = await getPreset("reviewer");
    expect(result.name).toBe("reviewer");
    expect(result.body).toBe("You are a reviewer");
  });
});
