import { describe, expect, it } from "vitest";
import { convertGrants, convertSummary } from "../convertProject.js";
import type { AgentConnector } from "../api/team.js";

const c = (id: string, extra: Partial<AgentConnector> = {}): AgentConnector => ({
  id, key: id, label: id, description: "", accounts: null, ops: null, ...extra,
});

describe("convertGrants", () => {
  it("grants every plain connector at Write and leaves tiers and tools alone", () => {
    expect(convertGrants([c("loki"), c("notes", { tier: "platform" }), c("tool:x", { tool: true })])).toEqual([
      { connector_id: "loki", accounts: [], level: "all", ops: [] },
    ]);
  });
});

describe("convertSummary", () => {
  it("names chats, channels and schedules", () => {
    expect(convertSummary(3, ["slack · C01"], 2)).toBe(
      "3 chats become the agent's; channels keep running as the agent: slack · C01; 2 schedules keep firing into it.",
    );
    expect(convertSummary(1, [], 0)).toBe("1 chat becomes the agent's.");
  });
  it("names the workflows bound to the project", () => {
    expect(convertSummary(2, [], 0, ["Nightly digest", "Triage"])).toBe(
      "2 chats become the agent's; workflows keep sending into it: Nightly digest, Triage.",
    );
  });
});
