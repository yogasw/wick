import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const mockFetch = vi.fn();
vi.stubGlobal("fetch", mockFetch);

function mockApp(base: string, projectId: string) {
  const el = document.createElement("div");
  el.id = "app";
  el.dataset.base = base;
  el.dataset.projectId = projectId;
  document.body.appendChild(el);
  return () => document.body.removeChild(el);
}

const BASE = "http://localhost:9425/tools/agents";

describe("getProjectSettings", () => {
  let cleanup: () => void;

  beforeEach(() => {
    cleanup = mockApp(BASE, "proj-abc");
    vi.resetModules();
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("returns project data for an existing project", async () => {
    const payload = {
      id: "proj-abc",
      name: "My Project",
      icon: "🚀",
      description: "desc",
      custom_path: "",
      managed: true,
      is_default: false,
      is_new: false,
      default_preset: "default",
      default_provider: "claude",
      system_addon: "",
      chat_count: 3,
      created_at: "2025-01-01",
      preset_list: ["default", "reviewer"],
      pinned: [{ id: "s1", label: "Session one" }],
      meta_json: "{}",
      action: `${BASE}/projects/proj-abc`,
    };
    mockFetch.mockResolvedValueOnce({ ok: true, json: async () => payload });
    const { getProjectSettings } = await import("$lib/api.js");
    const result = await getProjectSettings("proj-abc");
    expect(result.id).toBe("proj-abc");
    expect(result.name).toBe("My Project");
    expect(result.pinned).toHaveLength(1);
    expect(result.pinned[0].label).toBe("Session one");
    expect(result.managed).toBe(true);
  });

  it("returns empty/default shape for id=new", async () => {
    const payload = {
      id: "",
      name: "",
      icon: "📁",
      description: "",
      custom_path: "",
      managed: true,
      is_default: false,
      is_new: true,
      default_preset: "default",
      default_provider: "claude",
      system_addon: "",
      chat_count: 0,
      created_at: "",
      preset_list: ["default"],
      pinned: [],
      meta_json: "",
      action: `${BASE}/projects`,
    };
    mockFetch.mockResolvedValueOnce({ ok: true, json: async () => payload });
    const { getProjectSettings } = await import("$lib/api.js");
    const result = await getProjectSettings("new");
    expect(result.is_new).toBe(true);
    expect(result.pinned).toEqual([]);
    expect(result.icon).toBe("📁");
  });

  it("throws ApiError on non-ok response", async () => {
    mockFetch.mockResolvedValueOnce({ ok: false, status: 404, text: async () => "not found" });
    const { getProjectSettings, ApiError } = await import("$lib/api.js");
    await expect(getProjectSettings("missing")).rejects.toBeInstanceOf(ApiError);
  });
});

describe("updateProject", () => {
  let cleanup: () => void;

  beforeEach(() => {
    cleanup = mockApp(BASE, "proj-abc");
    vi.resetModules();
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("POSTs JSON to /api/projects/{id}", async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: async () => ({ status: "ok" }) });
    const { updateProject } = await import("$lib/api.js");
    await updateProject("proj-abc", {
      name: "Updated",
      icon: "🔧",
      description: "new desc",
      folder_mode: "managed",
      custom_path: "",
      preset: "default",
      provider: "claude",
      system_addon: "",
    });
    expect(mockFetch).toHaveBeenCalledOnce();
    const [url, opts] = mockFetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe(`${BASE}/api/projects/proj-abc`);
    expect(opts.method).toBe("POST");
    expect(opts.headers).toMatchObject({ "Content-Type": "application/json" });
    const body = JSON.parse(opts.body as string);
    expect(body.name).toBe("Updated");
  });
});

describe("deleteProject", () => {
  let cleanup: () => void;

  beforeEach(() => {
    cleanup = mockApp(BASE, "proj-abc");
    vi.resetModules();
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("sends DELETE to /projects/{id}", async () => {
    mockFetch.mockResolvedValueOnce({ ok: true });
    const { deleteProject } = await import("$lib/api.js");
    await deleteProject("proj-abc");
    const [url, opts] = mockFetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe(`${BASE}/projects/proj-abc`);
    expect(opts.method).toBe("DELETE");
  });

  it("throws ApiError on failure", async () => {
    mockFetch.mockResolvedValueOnce({ ok: false, status: 500, text: async () => "err" });
    const { deleteProject, ApiError } = await import("$lib/api.js");
    await expect(deleteProject("proj-abc")).rejects.toBeInstanceOf(ApiError);
  });
});

describe("unpinSession", () => {
  let cleanup: () => void;

  beforeEach(() => {
    cleanup = mockApp(BASE, "proj-abc");
    vi.resetModules();
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("sends form POST with unpin=<sid> to /projects/{id}", async () => {
    mockFetch.mockResolvedValueOnce({ ok: true });
    const { unpinSession } = await import("$lib/api.js");
    await unpinSession("proj-abc", "sess-1");
    const [url, opts] = mockFetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe(`${BASE}/projects/proj-abc`);
    expect(opts.method).toBe("POST");
    expect(opts.body?.toString()).toContain("unpin=sess-1");
  });
});
