import { describe, it, expect, vi, beforeEach } from "vitest";
import { fetchOverview, fetchTeam } from "../api.js";

beforeEach(() => {
  vi.resetAllMocks();
});

describe("fetchOverview - null normalization", () => {
  it("normalizes null queued to empty array", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ queued: null, active: [], stats: { active: 0, pool_max: 4, queue_len: 0 } }),
    }));
    const r = await fetchOverview("/tools/agents");
    expect(r.queued).toEqual([]);
  });

  it("normalizes null active to empty array", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ queued: [], active: null, stats: { active: 0, pool_max: 4, queue_len: 0 } }),
    }));
    const r = await fetchOverview("/tools/agents");
    expect(r.active).toEqual([]);
  });

  it("normalizes null stats to defaults", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ queued: [], active: [], stats: null }),
    }));
    const r = await fetchOverview("/tools/agents");
    expect(r.stats).toEqual({ active: 0, pool_max: 0, queue_len: 0 });
  });

  it("passes through valid data unchanged", async () => {
    const payload = {
      queued: [{ session_id: "abc", agent_name: "claude", waiting_ms: 5000, label: "hello", project: "proj" }],
      active: [{ session_id: "def", label: "world", lifecycle: "working", pid: 1234, project_id: "" }],
      stats: { active: 1, pool_max: 4, queue_len: 1 },
    };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => payload,
    }));
    const r = await fetchOverview("/tools/agents");
    expect(r.queued[0].session_id).toBe("abc");
    expect(r.active[0].session_id).toBe("def");
    expect(r.stats.active).toBe(1);
    expect(r.stats.pool_max).toBe(4);
  });
});

describe("fetchTeam", () => {
  it("reads the team roster from the team API", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ agents: [{ id: "a1", handle: "captain" }], captain_id: "a1" }),
    });
    vi.stubGlobal("fetch", fetchMock);
    const r = await fetchTeam("/tools/agents");
    expect(fetchMock.mock.calls[0][0]).toBe("/tools/agents/api/team/agents?ensure=0");
    expect(r.captain_id).toBe("a1");
    expect(r.agents).toHaveLength(1);
  });

  it("normalizes a null roster and missing captain", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ agents: null }),
    }));
    expect(await fetchTeam("/tools/agents")).toEqual({ agents: [], captain_id: "" });
  });

  it("throws on a failed request", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false,
      status: 503,
      text: async () => "team not ready",
    }));
    await expect(fetchTeam("/tools/agents")).rejects.toThrow("team not ready");
  });
});
