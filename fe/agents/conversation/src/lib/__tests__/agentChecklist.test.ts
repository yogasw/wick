import { describe, it, expect } from "vitest";
import {
  newGrant, opStats, checkedCount, selectAll, clearAll, accountTicked, toggleAccount,
  pruneGrants, parseGrantErrors, writeConnectors,
} from "../agentForm.js";
import type { AgentConnector, ConnectorGrant } from "../api/team.js";

const conn = (id: string, ops: [string, boolean][] = [], accounts: string[] = []): AgentConnector => ({
  id, key: id, label: id.toUpperCase(), description: "",
  accounts: accounts.map((a) => ({ id: a, display_name: a || "bot / instance" })),
  ops: ops.map(([key, destructive]) => ({ key, name: key, destructive })),
});
const g = (id: string, over: Partial<ConnectorGrant> = {}): ConnectorGrant => ({ ...newGrant(id), ...over });

const slack = conn("c1", [["read_thread", false], ["send_message", true], ["nuke", true]], ["", "acc-1", "acc-2"]);
const loki = conn("c2", [["query", false]]);
const catalog = [slack, loki];

describe("newGrant", () => {
  it("defaults to read-only on every account", () => {
    expect(newGrant("c1")).toEqual({ connector_id: "c1", accounts: [], level: "read", ops: [] });
  });
});

describe("opStats / checkedCount", () => {
  it("counts ops and write ops", () => {
    expect(opStats(slack)).toEqual({ total: 3, write: 2 });
    expect(opStats({ ...loki, ops: null })).toEqual({ total: 0, write: 0 });
  });
  it("counts only grants the catalog still lists", () => {
    expect(checkedCount([g("c1"), g("gone")], catalog)).toEqual({ checked: 1, total: 2 });
  });
});

describe("selectAll / clearAll", () => {
  it("adds missing connectors read-only and keeps existing levels", () => {
    const out = selectAll([g("c1", { level: "all" })], catalog);
    expect(out).toEqual([g("c1", { level: "all" }), g("c2")]);
  });
  it("clears only the visible connectors", () => {
    expect(clearAll([g("c1"), g("c2")], [loki])).toEqual([g("c1")]);
  });
});

describe("account chips", () => {
  const all = ["", "acc-1", "acc-2"];
  it("empty list ticks every chip", () => {
    expect(accountTicked(g("c1"), "acc-2")).toBe(true);
    expect(accountTicked(g("c1", { accounts: ["acc-1"] }), "acc-2")).toBe(false);
  });
  it("unticking from 'all' lists the rest explicitly", () => {
    expect(toggleAccount(g("c1"), all, "acc-1", false)).toEqual(["", "acc-2"]);
  });
  it("ticking the last missing one collapses to []", () => {
    expect(toggleAccount(g("c1", { accounts: ["", "acc-2"] }), all, "acc-1", true)).toEqual([]);
  });
  it("refuses to untick the final chip", () => {
    expect(toggleAccount(g("c1", { accounts: ["acc-1"] }), all, "acc-1", false)).toBeNull();
  });
});

describe("pruneGrants", () => {
  it("drops connectors, accounts and ops the catalog no longer has", () => {
    const { grants, dropped } = pruneGrants(
      [g("c1", { accounts: ["", "acc-x"], level: "pick", ops: ["send_message", "gone_op"] }), g("c9")],
      catalog,
    );
    expect(grants).toEqual([g("c1", { accounts: [""], level: "pick", ops: ["send_message"] })]);
    expect(dropped).toBe(3);
  });
  it("leaves a clean list alone", () => {
    expect(pruneGrants([g("c2")], catalog)).toEqual({ grants: [g("c2")], dropped: 0 });
  });
});

describe("parseGrantErrors", () => {
  it("spreads every rejected item onto its row", () => {
    const e = parseGrantErrors(
      "allowed_connectors: outside your connector access: connector c9, account c1/acc-x, op c1/nuke",
      catalog,
    );
    expect(e.missing).toEqual(["c9"]);
    expect(e.byConnector.c1).toEqual([
      "akun acc-x tidak lagi bisa kamu akses",
      "operasi nuke tidak aktif atau tidak bisa kamu akses",
    ]);
    expect(e.general).toBe("");
  });
  it("routes run_as and other errors", () => {
    expect(parseGrantErrors("run_as must be caller or owner", catalog).runAs).toBe("run_as must be caller or owner");
    expect(parseGrantErrors("handle already taken", catalog).general).toBe("handle already taken");
  });
});

describe("writeConnectors", () => {
  it("names connectors with an allowed write op once", () => {
    expect(writeConnectors([g("c1", { level: "all" }), g("c2", { level: "all" })], catalog)).toEqual(["C1"]);
    expect(writeConnectors([g("c1", { level: "pick", ops: ["read_thread"] })], catalog)).toEqual([]);
    expect(writeConnectors([g("c1")], catalog)).toEqual([]);
  });
});
