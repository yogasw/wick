/* Access & roles: the mode presets of the new Access screen. A mode is
   only a shortcut that writes the grants an agent already stores
   (access_mode choose/owner, each grant's level and lend). Nothing here
   invents a permission the server does not have: per-person tiers
   (a guest treated differently from a teammate, write approval, custom
   tiers) are shown as "soon" until the backend has them. */
import type { ConnectorGrant } from "./api/team.js";

export type AccessMode = "choose" | "owner";
export type AccessPreset = "private" | "team" | "borrow" | "open" | "custom";

export const PRESETS: { id: AccessPreset; name: string; hint: string; soon?: string }[] = [
  { id: "private", name: "Private", hint: "Only the selected connectors; everyone uses their own access." },
  { id: "team", name: "Team", hint: "All your connectors; everyone uses their own access." },
  { id: "borrow", name: "Lend", hint: "People without access use your account, read only." },
  { id: "open", name: "Open", hint: "Anyone uses a shared account, narrow list.", soon: "needs a guest tier" },
  { id: "custom", name: "Custom", hint: "Set each connector yourself in the table." },
];

/** detectPreset names the mode a stored access matches; anything that is
    not exactly one preset reads as "custom", so an old agent shows what
    it really has instead of being forced into a mode. */
export function detectPreset(mode: AccessMode, grants: ConnectorGrant[]): AccessPreset {
  const lent = grants.filter((g) => g.lend);
  if (mode === "owner") return lent.length === 0 ? "team" : "custom";
  if (lent.length === 0) return "private";
  return lent.every((g) => g.level === "read") ? "borrow" : "custom";
}

/** applyPreset is the grants a preset writes. Private and Team clear every
    lend (keeping each connector's level); Borrow lends every connector
    that is on at read level. Open and Custom change nothing by
    themselves: Open needs a guest tier the server does not have yet, and
    Custom is edited row by row. */
export function applyPreset(p: AccessPreset, mode: AccessMode, grants: ConnectorGrant[]): { mode: AccessMode; grants: ConnectorGrant[] } {
  const copy = grants.map((g) => ({ ...g, accounts: [...(g.accounts ?? [])], ops: [...(g.ops ?? [])] }));
  switch (p) {
    case "private":
      return { mode: "choose", grants: copy.map(({ lend: _l, ...g }) => g) };
    case "team":
      return { mode: "owner", grants: copy.map(({ lend: _l, ...g }) => g) };
    case "borrow":
      return { mode: "choose", grants: copy.map((g) => (g.level === "off" ? g : { ...g, level: "read", lend: true })) };
  }
  return { mode, grants: copy };
}

const LEVEL_NAME: Record<string, string> = { off: "off", read: "read", pick: "selected ops", all: "all" };
export const levelName = (lv: string) => LEVEL_NAME[lv] ?? lv;

/** effectiveLevel is the level the draft gives a connector, the way the
    server's Scope reads it for a Connectors-tier row: an explicit grant,
    else "all" under owner mode, else off. */
export function effectiveLevel(mode: AccessMode, g: ConnectorGrant | undefined): string {
  if (g) return g.level === "all" || g.level === "pick" || g.level === "off" ? g.level : "read";
  return mode === "owner" ? "all" : "off";
}

/** cellFor says what one kind of caller gets on a connector under the
    draft: the level and who the call runs as. A lend applies to whoever
    cannot reach the connector themselves. */
export function cellFor(who: "owner" | "mate" | "guest", mode: AccessMode, g: ConnectorGrant | undefined): { level: string; as: string } {
  const level = effectiveLevel(mode, g);
  if (level === "off") return { level, as: "" };
  if (who === "owner") return { level, as: "as you" };
  if (g?.lend) return { level, as: who === "mate" ? "their own access; without it: your account" : "your account (lent)" };
  return who === "mate" ? { level, as: "their own access" } : { level: "off", as: "none" };
}

/** accessDiff lists, in words, what saving would change. */
export function accessDiff(
  before: { mode: AccessMode; grants: ConnectorGrant[] },
  after: { mode: AccessMode; grants: ConnectorGrant[] },
  label: (id: string) => string,
): string[] {
  const out: string[] = [];
  if (before.mode !== after.mode) out.push(`Access mode: ${before.mode === "owner" ? "All" : "Selected"} → ${after.mode === "owner" ? "All" : "Selected"}`);
  const b = new Map(before.grants.map((g) => [g.connector_id, g]));
  const a = new Map(after.grants.map((g) => [g.connector_id, g]));
  for (const id of new Set([...b.keys(), ...a.keys()])) {
    const x = b.get(id), y = a.get(id);
    const lx = effectiveLevel(before.mode, x), ly = effectiveLevel(after.mode, y);
    if (lx !== ly) out.push(`${label(id)}: ${levelName(lx)} → ${levelName(ly)}`);
    if (!!x?.lend !== !!y?.lend) out.push(`${label(id)}: ${y?.lend ? "lent" : "no longer lent"}`);
  }
  return out;
}

/** setRow writes one table edit (level and lend) into grants. */
export function setRow(grants: ConnectorGrant[], id: string, level: ConnectorGrant["level"], lend: boolean): ConnectorGrant[] {
  const i = grants.findIndex((g) => g.connector_id === id);
  const next: ConnectorGrant = { ...(i >= 0 ? grants[i] : { connector_id: id, accounts: [], ops: [] }), level };
  if (lend) next.lend = true;
  else delete next.lend;
  const out = [...grants];
  if (i >= 0) out[i] = next;
  else out.push(next);
  return out;
}
