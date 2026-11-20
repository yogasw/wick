import { describe, expect, it } from "vitest";
import type { RBACDoc } from "../api/team.js";
import {
  addRole, addRule, cellValue, lendConnector, moveRule, removeRole, removeRule, resetTools, roleNameError,
  setCell, setRole, setRuleRole, setTool, toolsLimited, toolsOf, unlendConnector,
} from "../rbacEdit.js";
import { whoIs } from "../rbacView.js";

const base = (): RBACDoc => ({
  roles: { trusted: { rank: 70, lend: "all" }, member: { rank: 40, lend: "read", write_needs_approval: true } },
  rules: [{ match: "shared", role: "member" }],
  lend: { music: { member: "all" } },
});

describe("rbacEdit", () => {
  it("never changes the document it is given", () => {
    const d = base();
    const snap = JSON.stringify(d);
    addRole(d, "support", 60, "read");
    addRule(d, { match: "user", value: "u1", role: "trusted" });
    setCell(d, "guest", "music", "all");
    removeRole(d, "member");
    expect(JSON.stringify(d)).toBe(snap);
  });

  it("checks a new role name", () => {
    expect(roleNameError(base(), "owner")).toContain("built in");
    expect(roleNameError(base(), "member")).toContain("already exists");
    expect(roleNameError(base(), "Ops!")).toContain("a-z");
    expect(roleNameError(base(), "support")).toBe("");
  });

  it("adds, changes and removes a role with its rules and cells", () => {
    let d = addRole(base(), "support", 150, "read");
    expect(d.roles.support).toEqual({ rank: 99, lend: "read" });
    d = setRole(d, "support", { lend: "all", write_needs_approval: true });
    expect(d.roles.support.write_needs_approval).toBe(true);
    d = addRule(d, { match: "tag", value: "tag-support", role: "support" });
    d = setCell(d, "support", "music", "read");
    d = removeRole(d, "support");
    expect(d.roles.support).toBeUndefined();
    expect(d.rules.some((r) => r.role === "support")).toBe(false);
    expect(d.lend.music.support).toBeUndefined();
  });

  it("orders, re-roles and drops rules; a shared rule keeps no value", () => {
    let d = addRule(base(), { match: "shared", value: "junk", role: "trusted" });
    expect(d.rules[1]).toEqual({ match: "shared", role: "trusted" });
    d = addRule(d, { match: "user", value: "u1", role: "owner" });
    d = moveRule(d, 2, -1);
    expect(d.rules[1].value).toBe("u1");
    d = setRuleRole(d, 1, "guest");
    expect(d.rules[1].role).toBe("guest");
    d = removeRule(d, 0);
    expect(d.rules.length).toBe(2);
  });

  it("lends to guest by name and back to default", () => {
    let d = lendConnector(base(), "repo");
    expect(d.lend.repo).toEqual({});
    d = setCell(d, "guest", "music", "all");
    expect(cellValue(d, "guest", "music")).toBe("all");
    d = setCell(d, "guest", "music", "default");
    expect(cellValue(d, "guest", "music")).toBe("default");
    d = unlendConnector(d, "repo");
    expect(d.lend.repo).toBeUndefined();
  });

  it("names tag rules in Who is who", () => {
    const d = addRule(base(), { match: "tag", value: "t1", role: "trusted" });
    expect(whoIs(d, (id) => id, (id) => (id === "t1" ? "Support" : id)).trusted).toEqual(["everyone tagged Support"]);
  });

  it("limits native tools per role; guest defaults without Bash, Edit and Write", () => {
    const d0 = base();
    expect(toolsOf(d0, "member")).toContain("Bash");
    expect(toolsOf(d0, "guest")).toEqual(["Read", "Grep", "Glob", "WebFetch", "WebSearch"]);
    let d = setTool(d0, "guest", "Grep", false);
    expect(d.tools?.guest).toEqual(["Read", "Glob", "WebFetch", "WebSearch"]);
    d = setTool(d, "member", "Write", false);
    expect(toolsLimited(d, "member")).toBe(true);
    expect(toolsOf(d, "member")).not.toContain("Write");
    d = removeRole(d, "member");
    expect(d.tools?.member).toBeUndefined();
    d = resetTools(d, "guest");
    expect(d.tools).toBeUndefined();
    expect(d0.tools).toBeUndefined();
  });
});
