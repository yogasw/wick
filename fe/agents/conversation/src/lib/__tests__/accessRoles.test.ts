import { describe, expect, it } from "vitest";
import { accessDiff, applyPreset, cellFor, detectPreset, setRow } from "../accessRoles.js";
import type { ConnectorGrant } from "../api/team.js";

const g = (id: string, level: ConnectorGrant["level"], lend = false): ConnectorGrant => ({ connector_id: id, accounts: [], ops: [], level, ...(lend ? { lend: true } : {}) });

describe("detectPreset", () => {
  it("maps stored access to the preset it matches, else custom", () => {
    expect(detectPreset("choose", [g("a", "all")])).toBe("private");
    expect(detectPreset("owner", [])).toBe("team");
    expect(detectPreset("choose", [g("a", "read", true)])).toBe("borrow");
    expect(detectPreset("choose", [g("a", "all", true)])).toBe("custom");
    expect(detectPreset("owner", [g("a", "read", true)])).toBe("custom");
  });
});

describe("applyPreset", () => {
  it("writes only the existing fields and keeps levels", () => {
    const start = [g("a", "all", true), g("b", "off")];
    expect(applyPreset("private", "owner", start)).toEqual({ mode: "choose", grants: [g("a", "all"), g("b", "off")] });
    expect(applyPreset("team", "choose", start)).toEqual({ mode: "owner", grants: [g("a", "all"), g("b", "off")] });
    expect(applyPreset("borrow", "owner", start)).toEqual({ mode: "choose", grants: [g("a", "read", true), g("b", "off")] });
  });
  it("leaves grants alone for open (not built) and custom", () => {
    const start = [g("a", "all")];
    expect(applyPreset("open", "choose", start)).toEqual({ mode: "choose", grants: start });
    expect(applyPreset("custom", "owner", start)).toEqual({ mode: "owner", grants: start });
  });
  it("round-trips: an applied preset is detected as itself", () => {
    for (const p of ["private", "team", "borrow"] as const) {
      const r = applyPreset(p, "choose", [g("a", "all"), g("b", "read", true)]);
      expect(detectPreset(r.mode, r.grants)).toBe(p);
    }
  });
});

describe("cellFor", () => {
  it("says who a call runs as", () => {
    expect(cellFor("owner", "choose", g("a", "read"))).toEqual({ level: "read", as: "sebagai kamu" });
    expect(cellFor("guest", "choose", g("a", "read"))).toEqual({ level: "off", as: "tidak ada" });
    expect(cellFor("guest", "choose", g("a", "read", true)).as).toBe("akunmu (dipinjam)");
    expect(cellFor("mate", "owner", undefined)).toEqual({ level: "all", as: "aksesnya sendiri" });
    expect(cellFor("owner", "choose", undefined)).toEqual({ level: "off", as: "" });
  });
});

describe("accessDiff and setRow", () => {
  it("lists every change in words, nothing when equal", () => {
    const before = { mode: "choose" as const, grants: [g("a", "read")] };
    expect(accessDiff(before, before, (id) => id)).toEqual([]);
    const after = { mode: "choose" as const, grants: setRow(before.grants, "a", "read", true) };
    expect(accessDiff(before, after, (id) => id.toUpperCase())).toEqual(["A: dipinjamkan"]);
    const added = setRow(before.grants, "b", "all", false);
    expect(accessDiff(before, { mode: "owner", grants: added }, (id) => id)).toContain("b: mati → semua");
  });
});
