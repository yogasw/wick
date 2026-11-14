/* By role access, editing: pure changes to a copy of the role document.
   Every function returns a NEW document and never touches its input, so
   the editor can cancel by dropping the draft. The server validates the
   whole document again on save (team.RBACDoc.Validate). */
import type { RBACDoc, RBACLevel, RBACRule } from "./api/team.js";

/** Built-in roles: never defined under roles. */
export const BUILT_IN = ["owner", "guest"] as const;

/** A plain level a table cell can be set to; "pick" keeps the picked ops. */
export type CellLevel = "off" | "read" | "all";

const clone = (d: RBACDoc): RBACDoc => JSON.parse(JSON.stringify(d)) as RBACDoc;

/** roleNameError says what is wrong with a new role name, "" when fine. */
export function roleNameError(doc: RBACDoc, name: string): string {
  const n = name.trim();
  if (!n) return "Give the role a name.";
  if ((BUILT_IN as readonly string[]).includes(n)) return `"${n}" is built in.`;
  if (!/^[a-z][a-z0-9-]{0,23}$/.test(n)) return "Use a-z, 0-9 or '-', starting with a letter, up to 24 characters.";
  if (doc.roles?.[n]) return `"${n}" already exists.`;
  return "";
}

/** addRole adds an owner-defined role between owner (100) and guest (0). */
export function addRole(doc: RBACDoc, name: string, rank: number, lend: CellLevel): RBACDoc {
  const d = clone(doc);
  d.roles = { ...(d.roles ?? {}), [name.trim()]: { rank: Math.min(99, Math.max(1, Math.round(rank))), lend } };
  return d;
}

/** setRole changes a role's rank or default lend. */
export function setRole(doc: RBACDoc, name: string, patch: { rank?: number; lend?: CellLevel; write_needs_approval?: boolean }): RBACDoc {
  const d = clone(doc);
  const r = d.roles?.[name];
  if (!r) return d;
  if (patch.rank !== undefined) r.rank = Math.min(99, Math.max(1, Math.round(patch.rank)));
  if (patch.lend !== undefined) r.lend = patch.lend;
  if (patch.write_needs_approval !== undefined) {
    if (patch.write_needs_approval) r.write_needs_approval = true;
    else delete r.write_needs_approval;
  }
  return d;
}

/** removeRole drops a role, the rules that give it and its lend cells. */
export function removeRole(doc: RBACDoc, name: string): RBACDoc {
  const d = clone(doc);
  delete d.roles[name];
  if (d.tools) delete d.tools[name];
  d.rules = (d.rules ?? []).filter((r) => r.role !== name);
  for (const row of Object.values(d.lend ?? {})) delete row[name];
  return d;
}

/** addRule appends a rule (first match wins, so the new one ranks last). */
export function addRule(doc: RBACDoc, rule: RBACRule): RBACDoc {
  const d = clone(doc);
  d.rules = [...(d.rules ?? []), rule.match === "shared" ? { match: "shared", role: rule.role } : { ...rule }];
  return d;
}

/** setRuleRole changes the role rule i gives. */
export function setRuleRole(doc: RBACDoc, i: number, role: string): RBACDoc {
  const d = clone(doc);
  if (d.rules?.[i]) d.rules[i].role = role;
  return d;
}

/** removeRule drops rule i. */
export function removeRule(doc: RBACDoc, i: number): RBACDoc {
  const d = clone(doc);
  d.rules = (d.rules ?? []).filter((_, j) => j !== i);
  return d;
}

/** moveRule moves rule i up (-1) or down (+1): the order is the priority. */
export function moveRule(doc: RBACDoc, i: number, by: -1 | 1): RBACDoc {
  const d = clone(doc);
  const j = i + by;
  if (!d.rules || j < 0 || j >= d.rules.length) return d;
  [d.rules[i], d.rules[j]] = [d.rules[j], d.rules[i]];
  return d;
}

/** lendConnector puts a connector in the lend table; each role then
    borrows at its default until a cell says otherwise. */
export function lendConnector(doc: RBACDoc, connectorID: string): RBACDoc {
  const d = clone(doc);
  d.lend = { ...(d.lend ?? {}) };
  if (!d.lend[connectorID]) d.lend[connectorID] = {};
  return d;
}

/** unlendConnector takes a connector out of the lend table. */
export function unlendConnector(doc: RBACDoc, connectorID: string): RBACDoc {
  const d = clone(doc);
  delete d.lend[connectorID];
  return d;
}

/** setCell sets what role borrows of connectorID. "default" drops the
    cell so the role's default applies (guest has none: default = off). */
export function setCell(doc: RBACDoc, role: string, connectorID: string, level: CellLevel | "default"): RBACDoc {
  const d = clone(doc);
  const row = (d.lend[connectorID] ??= {});
  if (level === "default") delete row[role];
  else row[role] = level as RBACLevel;
  return d;
}

/** cellValue is the editor's select value for a cell: the explicit level,
    "pick" for picked ops, or "default" when the role default applies. */
export function cellValue(doc: RBACDoc, role: string, connectorID: string): CellLevel | "pick" | "default" {
  const lv = doc.lend?.[connectorID]?.[role];
  if (lv === undefined) return "default";
  if (typeof lv === "object") return "pick";
  return lv;
}

/** The Tools & features switches a role can be limited to (same names and
    order as team.NativeTools on the server). */
export const TOOL_SWITCHES = ["Read", "Grep", "Glob", "WebFetch", "WebSearch", "Bash", "Edit", "Write"] as const;

/** What guest loses when the document does not list guest. */
export const GUEST_DENIED = ["Bash", "Edit", "Write"];

/** toolsLimited reports whether the document lists role under tools. */
export const toolsLimited = (doc: RBACDoc, role: string): boolean => !!doc.tools && role in doc.tools;

/** toolsOf is what role may use: its list, or the default when unlisted
    (every switch; guest without Bash, Edit and Write). */
export function toolsOf(doc: RBACDoc, role: string): string[] {
  if (toolsLimited(doc, role)) return TOOL_SWITCHES.filter((t) => doc.tools![role].includes(t));
  return TOOL_SWITCHES.filter((t) => role !== "guest" || !GUEST_DENIED.includes(t));
}

/** setTool turns one tool on or off for role, listing the role from its
    current tools first. */
export function setTool(doc: RBACDoc, role: string, tool: string, on: boolean): RBACDoc {
  const d = clone(doc);
  const cur = toolsOf(d, role).filter((t) => t !== tool);
  if (on) cur.push(tool);
  d.tools = { ...(d.tools ?? {}), [role]: TOOL_SWITCHES.filter((t) => cur.includes(t)) };
  return d;
}

/** resetTools drops role's list so the default applies again. */
export function resetTools(doc: RBACDoc, role: string): RBACDoc {
  const d = clone(doc);
  if (d.tools) {
    delete d.tools[role];
    if (Object.keys(d.tools).length === 0) delete d.tools;
  }
  return d;
}
