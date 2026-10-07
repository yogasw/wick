/* skill.ts — a read of a SKILL.md renders as a skill card (SkillBlock)
   instead of a numbered code dump: name and description from the
   frontmatter, where the skill lives (from its path), the wick-managed
   warning comment turned into a badge, and the body as markdown.

   FE-only on purpose: the backend still stamps the read as code/markdown,
   so Raw stays the untouched event text and old traces get the card too. */
import type { TraceDisplay } from "./types.js";

/* builtin = shipped by wick (~/.support-tools/skills), global = a provider
   root in the home dir (~/.claude/skills, ~/.codex/skills…), project = a
   skills folder anywhere else (<pwd>/.claude/skills). "" = not known. */
export type SkillScope = "builtin" | "global" | "project" | "";

export type SkillLines = { from: number; to: number; partial: boolean };

export type SkillDoc = {
  name: string;
  description: string;
  scope: SkillScope;
  managed: boolean;
  /* The markdown after the managed comment and the frontmatter. */
  body: string;
  fields: Record<string, string>;
};

const SKILL_PATH_RE = /(^|[\\/])SKILL\.md$/i;

export function isSkillPath(p: string | undefined): boolean {
  return !!p && SKILL_PATH_RE.test(p.trim());
}

const HOME_RE = String.raw`(?:~|\$HOME|/root|/home/[^/]+|/Users/[^/]+)`;
const BUILTIN_RE = new RegExp(String.raw`(?:^|/)\.support-tools/skills/`);
const GLOBAL_RE = new RegExp(String.raw`^${HOME_RE}/\.(?:claude|codex|gemini|wick|agents)/skills/`);
const PROJECT_RE = /(?:^|\/)\.(?:claude|codex|gemini|wick|agents)\/skills\//;

export function skillScope(p: string | undefined): SkillScope {
  const n = (p ?? "").trim().replace(/\\/g, "/");
  if (!n) return "";
  if (BUILTIN_RE.test(n)) return "builtin";
  if (GLOBAL_RE.test(n)) return "global";
  if (PROJECT_RE.test(n)) return "project";
  return "";
}

export const SCOPE_LABEL: Record<Exclude<SkillScope, "">, string> = {
  builtin: "Built-in wick",
  global: "Global",
  project: "Project",
};

/* The folder a SKILL.md sits in — the skill's name when it has no
   frontmatter (or the read started past it). */
export function skillFolder(p: string | undefined): string {
  const parts = (p ?? "").replace(/\\/g, "/").split("/").filter(Boolean);
  return parts.length >= 2 ? parts[parts.length - 2] : "";
}

/* Claude's Read numbers every line ("    12\tcode", older builds "12→").
   Returns the file text without them and the line range shown; text that
   is not numbered comes back as is with from = 0. Anything after the
   numbered run (a <system-reminder> the harness appended) is not file
   content and is dropped. */
const NUMBERED_RE = /^\s*(\d+)(?:\t|→)(.*)$/;

export function stripLineNumbers(text: string): { text: string; from: number; to: number } {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const first = NUMBERED_RE.exec(lines[0] ?? "");
  if (!first) return { text, from: 0, to: 0 };
  const out: string[] = [];
  const from = Number(first[1]);
  let to = from;
  for (const l of lines) {
    const m = NUMBERED_RE.exec(l);
    if (!m) break;
    to = Number(m[1]);
    out.push(m[2]);
  }
  return { text: out.join("\n"), from, to };
}

const MANAGED_RE = /^\s*<!--[\s\S]*?-->\s*\n?/;

/* A leading "<!-- MANAGED BY WICK — DO NOT EDIT … -->" comment. */
export function stripManagedComment(text: string): { text: string; managed: boolean } {
  const m = MANAGED_RE.exec(text);
  if (!m || !/MANAGED BY WICK/i.test(m[0])) return { text, managed: false };
  return { text: text.slice(m[0].length), managed: true };
}

function unquote(v: string): string {
  const t = v.trim();
  if (t.length >= 2 && ((t[0] === '"' && t.endsWith('"')) || (t[0] === "'" && t.endsWith("'")))) return t.slice(1, -1);
  return t;
}

/* The flat YAML a SKILL.md frontmatter uses: `key: value`, quoted values,
   `>` / `|` block scalars and plain values continued on indented lines.
   Nested maps are kept as their raw text — the card only reads strings. */
export function parseFrontmatter(text: string): { fields: Record<string, string>; body: string } {
  const m = /^---[ \t]*\n([\s\S]*?)\n---[ \t]*(?:\n|$)/.exec(text);
  if (!m) return { fields: {}, body: text };
  const fields: Record<string, string> = {};
  let key = "", block = "", buf: string[] = [];
  const flush = () => {
    if (!key) return;
    const joined = block.startsWith("|") ? buf.join("\n") : buf.join(" ");
    fields[key] = block ? joined.trim() : unquote(joined);
  };
  for (const line of m[1].split("\n")) {
    const kv = /^([A-Za-z0-9_-]+):(?:\s+(.*))?$/.exec(line);
    if (kv) {
      flush();
      key = kv[1];
      const v = (kv[2] ?? "").trim();
      block = /^[|>][+-]?$/.test(v) ? v : "";
      buf = block || v === "" ? [] : [v];
      continue;
    }
    if (key) buf.push(block ? line.replace(/^\s{1,2}/, "") : line.trim());
  }
  flush();
  return { fields, body: text.slice(m[0].length) };
}

export function parseSkill(text: string, path?: string): SkillDoc {
  const { text: noComment, managed } = stripManagedComment(text);
  const { fields, body } = parseFrontmatter(noComment.replace(/^\s*\n/, ""));
  const scope = skillScope(path);
  return {
    name: fields.name || skillFolder(path) || "skill",
    description: fields.description ?? "",
    scope,
    managed: managed || scope === "builtin",
    body: body.replace(/^\s*\n/, ""),
    fields,
  };
}

/* offset/limit from the read call's input — a read that names either is
   partial even when it starts at line 1. */
function readWindow(callInput?: string): { offset?: number; limit?: number } {
  if (!callInput) return {};
  try {
    const v = JSON.parse(callInput);
    if (!v || typeof v !== "object") return {};
    const n = (x: unknown) => (typeof x === "number" ? x : undefined);
    return { offset: n(v.offset), limit: n(v.limit) };
  } catch {
    return {};
  }
}

const SKILL_TOOLS = new Set(["read", "read_file", "view", "readfile", "skill"]);

/* The display a result should render as: a "skill" card when it is the
   text of a SKILL.md, else null (keep what the classifier said). path is
   the result's, or the call's when the result has none. */
export function skillDisplay(
  d: TraceDisplay,
  toolName: string,
  callDisplay?: TraceDisplay | null,
  callInput?: string,
): TraceDisplay | null {
  if (!["code", "markdown", "text", "file"].includes(d.kind) || !d.body) return null;
  const tool = toolName.trim().toLowerCase();
  if (!SKILL_TOOLS.has(tool)) return null;
  const path = d.path || callDisplay?.path || "";
  const { text, from, to } = stripLineNumbers(d.body);
  // The Skill tool names no path: only its output's shape says it is a skill.
  if (tool === "skill" ? !/^\s*(?:<!--|---\s*\n)/.test(text) : !isSkillPath(path)) return null;
  const w = readWindow(callInput);
  const offset = w.offset ?? Number(/ @(\d+)$/.exec(callDisplay?.summary ?? "")?.[1] ?? 0);
  const lines: SkillLines | undefined = from
    ? { from, to, partial: from > 1 || offset > 1 || w.limit !== undefined }
    : undefined;
  return { ...d, kind: "skill", lang: "markdown", path: path || undefined, body: text, lines };
}
