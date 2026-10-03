import { describe, expect, it } from "vitest";
import { personaInput, suggestedConnectors } from "../personaGen.js";
import type { AgentConnector } from "../api/team.js";

const conn = (id: string, key: string, extra: Partial<AgentConnector> = {}): AgentConnector => ({
  id, key, label: key.toUpperCase(), description: "", accounts: null, ops: null, ...extra,
});
const catalog = [conn("c1", "loki"), conn("c2", "github"), conn("t1", "wick_notes", { tier: "platform" }), conn("tool:x", "x", { tool: true })];

describe("personaInput", () => {
  it("sends the target, non-empty fields and only grantable connector keys", () => {
    expect(personaInput("system_prompt", "  ", { name: "Anton", tagline: "", system_prompt: "Cek log" }, catalog)).toEqual({
      text: "",
      fields: { target: "system_prompt", name: "Anton", system_prompt: "Cek log", connectors: "loki, github" },
    });
  });

  it("omits connectors when the catalog has none", () => {
    expect(personaInput("all", " review PRs ", {}, [])).toEqual({ text: "review PRs", fields: { target: "all" } });
  });
});

describe("suggestedConnectors", () => {
  it("keeps known, ungranted, grantable entries once, in order", () => {
    const got = suggestedConnectors(["github", "nope", "loki", "github", "wick_notes", "x"], catalog, ["c1"]);
    expect(got.map((c) => c.id)).toEqual(["c2"]);
  });

  it("tolerates a missing list", () => {
    expect(suggestedConnectors(null, catalog, [])).toEqual([]);
  });
});
