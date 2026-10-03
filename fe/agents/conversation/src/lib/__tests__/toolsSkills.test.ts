import { describe, expect, it } from "vitest";
import { bashNote, bashPatternError, bashScopeError, enforcementNote, nativeToolsOf, toggleTool } from "../nativeTools.js";
import { createSkillPrompt, skillNote, toggleSkill } from "../agentSkills.js";

describe("native tools", () => {
  it("reads an absent field as every tool and keeps order", () => {
    expect(nativeToolsOf(undefined)).toContain("Bash");
    expect(nativeToolsOf(["Write", "Read"])).toEqual(["Read", "Write"]);
    expect(toggleTool(["Read"], "Bash", true)).toEqual(["Read", "Bash"]);
    expect(toggleTool(["Read", "Bash"], "Bash", false)).toEqual(["Read"]);
  });
  it("rejects chaining in a Bash pattern", () => {
    for (const p of ["ls | sh", "a; b", "make && x", "echo `id`", "echo $(id)"]) expect(bashPatternError(p)).not.toBe("");
    expect(bashPatternError("git status")).toBe("");
    expect(bashPatternError(" ")).not.toBe("");
    expect(bashScopeError("{project}")).toBe("");
    expect(bashScopeError("/srv")).toBe("");
    expect(bashScopeError("rel/dir")).not.toBe("");
  });
  it("warns only when the provider is not held to the switches", () => {
    expect(enforcementNote(true, "claude")).toBe("");
    expect(enforcementNote(undefined, "claude")).toBe("");
    expect(enforcementNote(false, "codex/work")).toMatch(/^Not enforced on codex/);
  });
  it("explains Bash with no rules as ask-first", () => {
    expect(bashNote(true, [])).toMatch(/asks you first/);
    expect(bashNote(false, [])).toMatch(/off/);
  });
});

describe("skills", () => {
  it("toggles and explains", () => {
    expect(toggleSkill(["b"], "a", false)).toEqual(["a", "b"]);
    expect(toggleSkill(["a", "b"], "a", true)).toEqual(["b"]);
    expect(skillNote({ name: "x", description: "", source: "builtin", shadowed: true, disabled: false })).toMatch(/local skill/);
    expect(skillNote({ name: "x", description: "", source: "local", overrides: "global", disabled: false })).toMatch(/Global/);
    expect(skillNote({ name: "x", description: "", source: "builtin", required: true, disabled: false })).toMatch(/always on/);
    expect(createSkillPrompt("/p/files/.claude/skills/")).toContain("/p/files/.claude/skills/<kebab-name>/SKILL.md");
  });
});
