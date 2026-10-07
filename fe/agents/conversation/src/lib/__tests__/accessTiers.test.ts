import { describe, expect, it } from "vitest";
import { overrideOf, setLevelFor, setOverride, tierDefault, toggleAccount } from "../accessTiers.js";
import type { AgentConnector } from "../api/team.js";

const conn = (id: string, tier: AgentConnector["tier"] = "", tool = false): AgentConnector =>
  ({ id, key: id, label: id, description: "", accounts: null, ops: null, tier, tool });

describe("accessTiers", () => {
  it("tier defaults", () => {
    expect(tierDefault(conn("n", "platform"), false)).toBe("all");
    expect(tierDefault(conn("w", "system"), false)).toBe("off");
    expect(tierDefault(conn("w", "system"), true)).toBe("all");
    expect(tierDefault(conn("s"), true)).toBe("off");
  });
  it("default drops the grant, others store it", () => {
    let g = setOverride([], "n", "off");
    expect(overrideOf(g, "n")).toBe("off");
    g = setOverride(g, "n", "default");
    expect(g).toEqual([]);
  });
  it("accounts are independent, none ticked drops the grant", () => {
    const all = ["", "me"];
    let g = toggleAccount([], "s", all, "me", true);
    expect(g[0].accounts).toEqual(["me"]);
    g = toggleAccount(g, "s", all, "", true);
    expect(g[0].accounts).toEqual([]);
    g = toggleAccount(g, "s", all, "me", false);
    expect(g[0].accounts).toEqual([""]);
    g = toggleAccount(g, "s", all, "", false);
    expect(g).toEqual([]);
  });
  it("bulk level", () => {
    const rows = [conn("a"), conn("t", "platform", true)];
    let g = setLevelFor([], rows, "read");
    expect(g.find((x) => x.connector_id === "t")?.level).toBe("all");
    g = setLevelFor(g, rows, "off");
    expect(g).toEqual([{ connector_id: "t", accounts: [], level: "off", ops: [] }]);
  });
});
