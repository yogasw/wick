/* By role access, read-only: turns an agent's role document into the rows
   the Roles section shows. Editing happens by chatting (agents.roles_set),
   so nothing here writes. */
import type { RBACDoc, RBACLevel } from "./api/team.js";

/** Roles in display order: owner, the owner's roles by rank (high first), guest. */
export function roleOrder(doc: RBACDoc): string[] {
  const mid = Object.entries(doc.roles ?? {})
    .sort((a, b) => b[1].rank - a[1].rank || a[0].localeCompare(b[0]))
    .map(([k]) => k);
  return ["owner", ...mid, "guest"];
}

export const roleTitle = (r: string) => r.charAt(0).toUpperCase() + r.slice(1);

/** whoIs lists, per role, who lands in it, in words. name maps a user id. */
export function whoIs(doc: RBACDoc, name: (id: string) => string): Record<string, string[]> {
  const out: Record<string, string[]> = { owner: ["You"] };
  for (const r of doc.rules ?? []) {
    const who = r.match === "shared" ? "everyone it's shared with" : name(r.value ?? "");
    (out[r.role] ??= []).push(who);
  }
  (out.guest ??= []).push("everyone else", "bots", "unknown callers");
  return out;
}

/** cellOf is what role borrows of connectorID, as the server's LendLevel
    reads it: owner has it as their own, guest never borrows, a role
    missing from the row borrows at its default. */
export function cellOf(doc: RBACDoc, role: string, connectorID: string): { text: string; tone: "on" | "read" | "off"; approval: boolean } {
  if (role === "owner") return { text: "Write", tone: "on", approval: false };
  const r = doc.roles?.[role];
  const row = doc.lend?.[connectorID];
  if (!r || !row) return { text: "Off", tone: "off", approval: false };
  const lv: RBACLevel = row[role] ?? r.lend;
  if (typeof lv === "object") return { text: `${lv.ops.length} ops`, tone: "read", approval: false };
  if (lv === "all") return r.write_needs_approval ? { text: "Read", tone: "read", approval: true } : { text: "Write", tone: "on", approval: false };
  if (lv === "read") return { text: "Read", tone: "read", approval: false };
  return { text: "Off", tone: "off", approval: false };
}
