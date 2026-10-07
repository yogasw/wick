/* Tools & features › Native tools and Bash commands (team/tools.go). */
import type { BashRule } from "./api/team.js";

export type NativeTool = { id: string; label: string; icon: string; hint: string; risky: boolean };

/** Same names and order as team.NativeTools. */
export const NATIVE_TOOLS: NativeTool[] = [
  { id: "Read", label: "Read", icon: "📄", hint: "Read files", risky: false },
  { id: "Grep", label: "Grep", icon: "🔎", hint: "Search file contents", risky: false },
  { id: "Glob", label: "Glob", icon: "🗂", hint: "Find files by name", risky: false },
  { id: "WebFetch", label: "WebFetch", icon: "🌐", hint: "Open a URL", risky: false },
  { id: "WebSearch", label: "WebSearch", icon: "🧭", hint: "Search the web", risky: false },
  { id: "Bash", label: "Bash", icon: "⌨", hint: "Run shell commands (listed ones run unasked, the rest ask you)", risky: true },
  { id: "Edit", label: "Edit", icon: "✏", hint: "Change existing files", risky: true },
  { id: "Write", label: "Write", icon: "💾", hint: "Create or overwrite files", risky: true },
];

/** What the server reads an absent field as: every tool (a row from
    before the setting). */
export function nativeToolsOf(v: string[] | null | undefined): string[] {
  return v ? NATIVE_TOOLS.map((t) => t.id).filter((id) => v.includes(id)) : NATIVE_TOOLS.map((t) => t.id);
}

/** toggleTool turns id on or off, keeping NATIVE_TOOLS order. */
export function toggleTool(tools: string[], id: string, on: boolean): string[] {
  const next = new Set(tools);
  if (on) next.add(id);
  else next.delete(id);
  return NATIVE_TOOLS.map((t) => t.id).filter((x) => next.has(x));
}

const CHAINING = ["|", ";", "&&", "`", "$("];
/** Characters the gate refuses in a pattern besides the chaining ones. */
const OTHER_META = [">", "<", "&", "\n"];

/** bashPatternError is the inline error for a pattern, "" when it saves. */
export function bashPatternError(pattern: string): string {
  const p = pattern.trim();
  if (!p) return "Write a command, e.g. git status or ls *.";
  const chain = CHAINING.find((c) => p.includes(c));
  if (chain) return `One command per rule — "${chain}" is not allowed.`;
  if (OTHER_META.some((c) => p.includes(c))) return "Redirects and background jobs are not allowed.";
  return "";
}

/** bashScopeError: "" / {project} / an absolute path. */
export function bashScopeError(scope: string): string {
  const s = scope.trim();
  if (!s || s === "{project}" || s.startsWith("/") || /^[A-Za-z]:[\\/]/.test(s)) return "";
  return "Use {project} or an absolute path.";
}

/** enforcementNote is the warning under Native tools, "" when the
    provider is held to the settings. */
export function enforcementNote(enforced: boolean | undefined, provider: string): string {
  if (enforced !== false) return "";
  const p = (provider || "this provider").split(/[/:]/)[0];
  return `Not enforced on ${p}: these switches and Bash rules are saved, but ${p} runs without them. Only claude is held to them.`;
}

/** bashNote explains what Bash does with the rules as they stand. */
export function bashNote(bashOn: boolean, rules: BashRule[]): string {
  if (!bashOn) return "Bash is off: the agent cannot run commands.";
  // With approvals off (gate off / bypass) nobody can be asked: Bash on then
  // runs every command, and these rules wait for approvals to come back.
  const off = " With approvals off, every command runs.";
  if (rules.length === 0) return "No commands listed: every command asks you first." + off;
  return "Listed commands run without asking; any other command asks you first." + off;
}
