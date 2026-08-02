import { describe, test, expect, vi, afterEach } from "vitest";
import {
  canLeadDelegation,
  emptyAgentProfile,
  listAgentProfiles,
  shadowedKeys,
  type AgentProfile,
  type AgentProfileList,
} from "../agentProfiles.js";

const originalFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = originalFetch;
  vi.restoreAllMocks();
});

// jsdom resolves request URLs against the document origin, so the raw
// string is absolute. Compare the path + query, which is what the client
// actually builds.
function captureFetch(body: unknown) {
  const calls: string[] = [];
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const raw = typeof input === "string" ? input : input.toString();
    const u = new URL(raw, "http://localhost");
    calls.push(u.pathname + u.search);
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as unknown as typeof fetch;
  return calls;
}

function prof(overrides: Partial<AgentProfile>): AgentProfile {
  return { ...emptyAgentProfile(), ...overrides };
}

describe("listAgentProfiles", () => {
  // An empty scope must OMIT the parameter, not send it blank: on a
  // server that distinguishes the two, "?project_id=" reads as a project
  // whose id is the empty string.
  test("omits project_id entirely for the global scope", async () => {
    const calls = captureFetch({ profiles: [], owned: [], inherited: [] });
    await listAgentProfiles("/tools/agents");
    expect(calls[0]).toBe("/tools/agents/api/agent-profiles");
  });

  test("sends project_id for a project scope", async () => {
    const calls = captureFetch({ profiles: [], owned: [], inherited: [] });
    await listAgentProfiles("/tools/agents", "proj-abc");
    expect(calls[0]).toBe("/tools/agents/api/agent-profiles?project_id=proj-abc");
  });

  // Every consumer iterates these arrays directly; an undefined would
  // crash the render rather than showing an empty list.
  test("missing arrays default to empty, never undefined", async () => {
    captureFetch({});
    const got = await listAgentProfiles("/tools/agents", "proj-abc");
    expect(got.profiles).toEqual([]);
    expect(got.owned).toEqual([]);
    expect(got.inherited).toEqual([]);
  });
});

describe("shadowedKeys", () => {
  test("flags an owned role that reuses a global key", () => {
    const list: AgentProfileList = {
      profiles: [],
      owned: [prof({ key: "researcher", project_id: "proj-abc" }), prof({ key: "db-migrator", project_id: "proj-abc" })],
      inherited: [prof({ key: "researcher" }), prof({ key: "reviewer" })],
    };
    const got = shadowedKeys(list);
    expect([...got]).toEqual(["researcher"]);
  });

  test("a project-only role is not a shadow", () => {
    const list: AgentProfileList = {
      profiles: [],
      owned: [prof({ key: "db-migrator", project_id: "proj-abc" })],
      inherited: [prof({ key: "researcher" })],
    };
    expect(shadowedKeys(list).size).toBe(0);
  });
});

describe("canLeadDelegation", () => {
  // Mirrors leaderCapableProviders on the server. If these drift, the
  // form offers a checkbox whose value the server silently discards.
  test("matches the server's leader-capable set", () => {
    expect(canLeadDelegation("claude")).toBe(true);
    expect(canLeadDelegation("codex")).toBe(true);
    expect(canLeadDelegation("wick")).toBe(true);
    expect(canLeadDelegation("gemini")).toBe(false);
  });
});

describe("emptyAgentProfile", () => {
  // strict_mcp defaults ON: a new role should not reach the host's own
  // MCP servers unless someone deliberately turns the guard off.
  test("defaults to the safe end of every switch", () => {
    const p = emptyAgentProfile();
    expect(p.strict_mcp).toBe(true);
    expect(p.can_delegate).toBe(false);
    expect(p.allow_take_over).toBe(false);
    expect(p.disabled).toBe(false);
  });

  test("carries the scope it was created in", () => {
    expect(emptyAgentProfile("proj-abc").project_id).toBe("proj-abc");
    expect(emptyAgentProfile().project_id).toBe("");
  });
});
