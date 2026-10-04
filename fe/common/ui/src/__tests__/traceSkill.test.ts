import { describe, it, expect, afterEach } from "vitest";
import { render, fireEvent, cleanup } from "@testing-library/svelte";
import TraceBody from "../trace/TraceBody.svelte";
import { classifyCall } from "../trace/classify.js";
import {
  isSkillPath, parseFrontmatter, parseSkill, skillDisplay, skillFolder, skillScope, stripLineNumbers, stripManagedComment,
} from "../trace/skill.js";

afterEach(() => cleanup());

const MANAGED = [
  "<!-- MANAGED BY WICK — DO NOT EDIT.",
  "This file ships inside the wick binary. -->",
  "---",
  "name: demo-skill",
  "description: Use when the demo needs a demo.",
  "---",
  "",
  "# Demo skill",
  "",
  "- step one",
  "- step two",
].join("\n");

// Claude's Read output: right-aligned number, tab, the line.
const numbered = (text: string, from = 1) =>
  text.split("\n").map((l, i) => `${String(from + i).padStart(6)}\t${l}`).join("\n");

const call = (input: object) => classifyCall("Read", JSON.stringify(input));

const BUILTIN = "/home/u/.support-tools/skills/demo-skill/SKILL.md";
const PROJECT = "/home/u/.support-tools/agents/projects/p1/files/.claude/skills/demo-skill/SKILL.md";

describe("skill path + scope", () => {
  it("detects SKILL.md only", () => {
    expect(isSkillPath(BUILTIN)).toBe(true);
    expect(isSkillPath("SKILL.md")).toBe(true);
    expect(isSkillPath("/r/docs/SKILL.md.bak")).toBe(false);
    expect(isSkillPath("/r/README.md")).toBe(false);
    expect(isSkillPath(undefined)).toBe(false);
  });

  it("scope from the path", () => {
    expect(skillScope(BUILTIN)).toBe("builtin");
    expect(skillScope("~/.support-tools/skills/x/SKILL.md")).toBe("builtin");
    expect(skillScope("/home/u/.claude/skills/x/SKILL.md")).toBe("global");
    expect(skillScope("~/.codex/skills/x/SKILL.md")).toBe("global");
    expect(skillScope("/root/.claude/skills/x/SKILL.md")).toBe("global");
    expect(skillScope(PROJECT)).toBe("project");
    expect(skillScope("/srv/repo/.claude/skills/x/SKILL.md")).toBe("project");
    expect(skillScope("/srv/repo/skills/x/SKILL.md")).toBe("");
    expect(skillFolder(PROJECT)).toBe("demo-skill");
  });
});

describe("skill text", () => {
  it("strips Read line numbers and drops what follows the numbered run", () => {
    const r = stripLineNumbers(numbered("a\n\tb\nc", 40) + "\n\n<system-reminder>x</system-reminder>");
    expect(r).toEqual({ text: "a\n\tb\nc", from: 40, to: 42 });
    expect(stripLineNumbers("12→old arrow form").text).toBe("old arrow form");
    expect(stripLineNumbers("plain text")).toEqual({ text: "plain text", from: 0, to: 0 });
  });

  it("strips only the wick-managed comment", () => {
    expect(stripManagedComment(MANAGED).managed).toBe(true);
    expect(stripManagedComment(MANAGED).text.startsWith("---\nname: demo-skill")).toBe(true);
    const other = "<!-- a note -->\n# Title";
    expect(stripManagedComment(other)).toEqual({ text: other, managed: false });
  });

  it("parses flat frontmatter, quotes and block scalars", () => {
    const { fields, body } = parseFrontmatter(
      "---\nname: 'x'\ndescription: >\n  folded one\n  folded two\nnotes: |\n  a\n  b\nlong: first\n  second\n---\n# Body",
    );
    expect(fields).toEqual({ name: "x", description: "folded one folded two", notes: "a\nb", long: "first second" });
    expect(body).toBe("# Body");
    expect(parseFrontmatter("# no frontmatter").fields).toEqual({});
  });

  it("parseSkill: name/description, managed badge, body without comment or frontmatter", () => {
    const s = parseSkill(MANAGED, BUILTIN);
    expect(s).toMatchObject({ name: "demo-skill", description: "Use when the demo needs a demo.", scope: "builtin", managed: true });
    expect(s.body.startsWith("# Demo skill")).toBe(true);
    expect(s.body).not.toContain("MANAGED BY WICK");
    // A built-in skill is managed even when the read started past the comment.
    expect(parseSkill("# Part", BUILTIN).managed).toBe(true);
    // No frontmatter: the folder names the skill.
    expect(parseSkill("# Part", PROJECT)).toMatchObject({ name: "demo-skill", description: "", scope: "project", managed: false });
  });
});

describe("skillDisplay", () => {
  it("turns a Read of SKILL.md into a full (not partial) skill card", () => {
    const d = skillDisplay({ kind: "markdown", body: numbered(MANAGED) }, "Read", call({ file_path: BUILTIN }));
    expect(d).toMatchObject({ kind: "skill", path: BUILTIN, body: MANAGED, lines: { from: 1, to: 11, partial: false } });
  });

  it("marks offset/limit reads partial", () => {
    const byNumbers = skillDisplay({ kind: "code", body: numbered("# Later", 30) }, "Read", call({ file_path: PROJECT }));
    expect(byNumbers?.lines).toEqual({ from: 30, to: 30, partial: true });
    const input = JSON.stringify({ file_path: PROJECT, limit: 5 });
    const byLimit = skillDisplay({ kind: "code", body: numbered("---\nname: x", 1) }, "Read", classifyCall("Read", input), input);
    expect(byLimit?.lines?.partial).toBe(true);
  });

  it("leaves other files, errors and other tools alone", () => {
    expect(skillDisplay({ kind: "markdown", body: "# x" }, "Read", call({ file_path: "/r/README.md" }))).toBeNull();
    expect(skillDisplay({ kind: "error", body: "no such file" }, "Read", call({ file_path: BUILTIN }))).toBeNull();
    expect(skillDisplay({ kind: "terminal", body: MANAGED, path: BUILTIN }, "Bash")).toBeNull();
  });

  it("Skill tool output with frontmatter is a card too", () => {
    expect(skillDisplay({ kind: "markdown", body: MANAGED }, "Skill")?.kind).toBe("skill");
    expect(skillDisplay({ kind: "text", body: "Launching skill: demo" }, "Skill")).toBeNull();
  });
});

describe("TraceBody renders a SKILL.md read as a skill card", () => {
  it("built-in managed skill: name, description, badges, markdown body, Raw keeps the original", async () => {
    const raw = numbered(MANAGED);
    const { container } = render(TraceBody, {
      raw, toolName: "Read", callDisplay: call({ file_path: BUILTIN }),
    });
    const q = (s: string) => container.querySelector(s);
    expect(q("[data-trace-body]")?.getAttribute("data-kind")).toBe("skill");
    expect(q("[data-skill-name]")?.textContent).toBe("demo-skill");
    expect(q("[data-skill-description]")?.textContent).toBe("Use when the demo needs a demo.");
    expect(q("[data-skill-scope]")?.textContent).toBe("Built-in wick");
    expect(q("[data-skill-managed]")?.textContent).toBe("Managed by wick — read-only");
    expect(q("[data-skill-partial]")).toBeNull();
    const md = q('[data-trace-kind="markdown"]');
    // The chat renderer draws headings as styled <p>, never as raw "# ".
    expect(md?.querySelector("p.font-semibold")?.textContent).toBe("Demo skill");
    expect(md?.textContent).not.toContain("# Demo");
    expect(md?.querySelectorAll("li").length).toBe(2);
    expect(container.textContent).not.toContain("MANAGED BY WICK");
    expect(container.textContent).not.toContain("name: demo-skill");

    await fireEvent.click(q("[data-trace-raw]")!);
    const rawText = q("[data-trace-raw-text]")?.textContent ?? "";
    expect(rawText).toContain("MANAGED BY WICK");
    expect(rawText).toContain("name: demo-skill");
  });

  it("partial project read: folder name, Project badge, partial range", () => {
    const { container } = render(TraceBody, {
      raw: numbered("## Step 3\n\nDo the thing.", 20), toolName: "Read", callDisplay: call({ file_path: PROJECT, offset: 20 }),
      callInput: JSON.stringify({ file_path: PROJECT, offset: 20 }),
    });
    expect(container.querySelector("[data-skill-name]")?.textContent).toBe("demo-skill");
    expect(container.querySelector("[data-skill-scope]")?.textContent).toBe("Project");
    expect(container.querySelector("[data-skill-managed]")).toBeNull();
    expect(container.querySelector("[data-skill-partial]")?.textContent).toBe("partial (lines 20–22)");
  });
});
