import { describe, it, expect, vi, beforeEach } from "vitest";
import { normalizeStorage, apiGetStorage, apiStorageRetention, apiStorageDelete, apiStorageRestore } from "../api.js";
import type { StorageResponse } from "../types.js";

beforeEach(() => {
  vi.resetAllMocks();
  document.getElementById = vi.fn().mockReturnValue({ dataset: { base: "/test" } });
});

function makeFile(overrides: Partial<StorageResponse["files"][0]> = {}): StorageResponse["files"][0] {
  return {
    id: 1,
    provider_type: "claude",
    instance_name: "default",
    rel_path: "config.json",
    name: "config.json",
    is_dir: false,
    size: 1024,
    synced_at: "2024-01-01T00:00:00Z",
    retention_days: 7,
    ...overrides,
  };
}

function makeStorage(overrides: Partial<StorageResponse> = {}): StorageResponse {
  return {
    files: [makeFile()],
    filter_provider: "",
    filter_instance: "",
    provider_types: ["claude", "openai"],
    ...overrides,
  };
}

describe("normalizeStorage - null normalization", () => {
  it("normalizes null files to empty array", () => {
    const raw = { ...makeStorage(), files: null } as unknown as StorageResponse;
    const r = normalizeStorage(raw);
    expect(r.files).toEqual([]);
  });

  it("normalizes null provider_types to empty array", () => {
    const raw = { ...makeStorage(), provider_types: null } as unknown as StorageResponse;
    const r = normalizeStorage(raw);
    expect(r.provider_types).toEqual([]);
  });

  it("normalizes null filter_provider to empty string", () => {
    const raw = { ...makeStorage(), filter_provider: null } as unknown as StorageResponse;
    const r = normalizeStorage(raw);
    expect(r.filter_provider).toBe("");
  });

  it("normalizes null filter_instance to empty string", () => {
    const raw = { ...makeStorage(), filter_instance: null } as unknown as StorageResponse;
    const r = normalizeStorage(raw);
    expect(r.filter_instance).toBe("");
  });

  it("normalizes null fields in a file row", () => {
    const raw = {
      ...makeStorage(),
      files: [{ id: null, provider_type: null, instance_name: null, rel_path: null, name: null, is_dir: null, size: null, synced_at: null, retention_days: null }],
    } as unknown as StorageResponse;
    const r = normalizeStorage(raw);
    expect(r.files[0].id).toBe(0);
    expect(r.files[0].provider_type).toBe("");
    expect(r.files[0].instance_name).toBe("");
    expect(r.files[0].rel_path).toBe("");
    expect(r.files[0].name).toBe("");
    expect(r.files[0].is_dir).toBe(false);
    expect(r.files[0].size).toBe(0);
    expect(r.files[0].synced_at).toBe("");
    expect(r.files[0].retention_days).toBe(0);
  });

  it("passes through valid data unchanged", () => {
    const payload = makeStorage();
    const r = normalizeStorage(payload);
    expect(r.files[0].id).toBe(1);
    expect(r.files[0].size).toBe(1024);
    expect(r.provider_types).toEqual(["claude", "openai"]);
  });
});

describe("apiGetStorage", () => {
  it("fetches /api/providers/storage and normalizes", async () => {
    const payload = makeStorage({ files: [makeFile({ id: 5, size: 2048 })] });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => payload,
    }));
    const r = await apiGetStorage();
    expect(r.files[0].id).toBe(5);
    expect(r.files[0].size).toBe(2048);
    const fetchMock = vi.mocked(global.fetch);
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/providers/storage"),
      expect.any(Object),
    );
  });

  it("appends provider filter query param", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => makeStorage(),
    }));
    await apiGetStorage("claude", "");
    const fetchMock = vi.mocked(global.fetch);
    const url = (fetchMock.mock.calls[0][0] as string);
    expect(url).toContain("provider=claude");
  });

  it("appends instance filter query param", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => makeStorage(),
    }));
    await apiGetStorage("", "default");
    const fetchMock = vi.mocked(global.fetch);
    const url = (fetchMock.mock.calls[0][0] as string);
    expect(url).toContain("instance=default");
  });

  it("throws ApiError on non-ok response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false,
      status: 503,
      text: async () => "service unavailable",
    }));
    await expect(apiGetStorage()).rejects.toThrow("service unavailable");
  });
});

describe("apiStorageRetention", () => {
  it("posts form-encoded days to /providers/storage/{id}/retention", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: { get: () => "application/json" },
      json: async () => ({ id: 1, retention_days: 7 }),
    }));
    await apiStorageRetention(1, 7);
    const fetchMock = vi.mocked(global.fetch);
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/providers/storage/1/retention"),
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("throws on error response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false,
      status: 400,
      text: async () => "invalid days",
    }));
    await expect(apiStorageRetention(1, -1)).rejects.toThrow("invalid days");
  });
});

describe("apiStorageDelete", () => {
  it("sends DELETE to /providers/storage/{id}", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: { get: () => "application/json" },
      json: async () => ({ status: "deleted" }),
    }));
    await apiStorageDelete(42);
    const fetchMock = vi.mocked(global.fetch);
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/providers/storage/42"),
      expect.objectContaining({ method: "DELETE" }),
    );
  });
});

describe("apiStorageRestore", () => {
  it("posts form-encoded ids to /providers/storage/restore", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ restored: 2 }),
    }));
    const r = await apiStorageRestore([1, 2]);
    expect(r.restored).toBe(2);
    const fetchMock = vi.mocked(global.fetch);
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/providers/storage/restore"),
      expect.objectContaining({ method: "POST" }),
    );
  });
});
