import { describe, test, expect } from "vitest";
import {
  accessPayload, bulkApply, filterCounts, filterRows, normalizeAccessMode, selectState, setRowLevel, toggleOne, toggleShown,
} from "../accessList.js";
import type { AgentConnector, ConnectorGrant } from "../api/team.js";

const c = (id: string, tier: AgentConnector["tier"] = "", extra: Partial<AgentConnector> = {}): AgentConnector => ({
  id, key: id, label: id, description: "", accounts: [], ops: [], tier, ...extra,
});
const g = (id: string, level: ConnectorGrant["level"], accounts: string[] = []): ConnectorGrant => ({ connector_id: id, accounts, level, ops: [] });
const rows = [c("a"), c("b"), c("d"), c("e")];

describe("access list", () => {
  test("filter chips split granted from not granted", () => {
    const grants = [g("a", "read"), g("d", "all")];
    expect(filterRows(rows, grants, "granted", false).map((x) => x.id)).toEqual(["a", "d"]);
    expect(filterRows(rows, grants, "not_granted", false).map((x) => x.id)).toEqual(["b", "e"]);
    expect(filterRows(rows, grants, "all", false)).toHaveLength(4);
    expect(filterCounts(rows, grants, false)).toEqual({ all: 4, granted: 2, not_granted: 2 });
  });

  test("tier rows count as granted by their default", () => {
    const tiered = [c("notes", "platform"), c("wm", "system")];
    expect(filterCounts(tiered, [], false)).toEqual({ all: 2, granted: 1, not_granted: 1 });
    expect(filterCounts(tiered, [], true).granted).toBe(2);
    expect(filterCounts(tiered, [g("notes", "off")], true).granted).toBe(1);
  });

  test("header checkbox selects every shown row, indeterminate when partial", () => {
    const shown = rows.slice(0, 2);
    let sel: Set<string> = new Set();
    expect(selectState(sel, shown)).toBe("none");
    sel = toggleOne(sel, "a", true);
    expect(selectState(sel, shown)).toBe("some");
    sel = toggleShown(sel, shown);
    expect([...sel].sort()).toEqual(["a", "b"]);
    expect(selectState(sel, shown)).toBe("all");
    // A hidden selected row stays selected when the shown ones are cleared.
    sel = toggleOne(sel, "e", true);
    sel = toggleShown(sel, shown);
    expect([...sel]).toEqual(["e"]);
    expect(selectState(new Set(), [])).toBe("none");
  });

  test("bulk actions touch the selected rows only", () => {
    const grants = [g("a", "read", ["acc-me"]), g("b", "all")];
    const sel = new Set(["a", "e"]);
    const w = bulkApply(grants, rows, sel, "all");
    expect(w.find((x) => x.connector_id === "a")).toEqual(g("a", "all", ["acc-me"]));
    expect(w.find((x) => x.connector_id === "e")?.level).toBe("all");
    expect(w.find((x) => x.connector_id === "b")?.level).toBe("all");
    expect(w.find((x) => x.connector_id === "d")).toBeUndefined();
    const r = bulkApply(grants, rows, new Set(["b"]), "read");
    expect(r.find((x) => x.connector_id === "b")?.level).toBe("read");
    expect(r.find((x) => x.connector_id === "a")?.level).toBe("read");
    const off = bulkApply(grants, rows, new Set(["a", "d"]), "off");
    expect(off.map((x) => x.connector_id)).toEqual(["b"]);
  });

  test("bulk on tier rows stores overrides; default resets them", () => {
    const tiered = [c("notes", "platform"), c("tool:todo", "platform", { tool: true })];
    const sel = new Set(["notes", "tool:todo"]);
    const off = bulkApply([], tiered, sel, "off");
    expect(off.map((x) => [x.connector_id, x.level])).toEqual([["notes", "off"], ["tool:todo", "off"]]);
    // wick's own tools have no read level.
    expect(bulkApply([], tiered, sel, "read").find((x) => x.connector_id === "tool:todo")?.level).toBe("all");
    expect(bulkApply(off, tiered, new Set(["notes"]), "default").map((x) => x.connector_id)).toEqual(["tool:todo"]);
  });

  test("row level: off clears a connector, keeps an override on a tier row", () => {
    expect(setRowLevel([g("a", "read")], c("a"), "off")).toEqual([]);
    expect(setRowLevel([], c("notes", "platform"), "off")).toEqual([g("notes", "off")]);
    expect(setRowLevel([g("a", "read", ["acc-me"])], c("a"), "all")).toEqual([g("a", "all", ["acc-me"])]);
  });

  test("Same as me keeps the checklist in the payload", () => {
    const grants = [g("a", "read")];
    expect(accessPayload("owner", grants, false)).toEqual({ access_mode: "owner", allowed_connectors: grants, include_new_connectors: false });
    expect(accessPayload("choose", grants, true).access_mode).toBe("choose");
    expect(normalizeAccessMode(undefined)).toBe("choose");
    expect(normalizeAccessMode("owner")).toBe("owner");
    expect(normalizeAccessMode("weird")).toBe("choose");
  });
});
