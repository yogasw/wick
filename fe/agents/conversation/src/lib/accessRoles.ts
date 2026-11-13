/* Access & roles: the mode presets of the new Access screen. A mode is
   only a shortcut that writes the grants an agent already stores
   (access_mode choose/owner, each grant's level and lend). Nothing here
   invents a permission the server does not have: per-person tiers
   (a guest treated differently from a teammate, write approval, custom
   tiers) are shown as "segera" (coming soon) until the backend has them. */
import type { ConnectorGrant } from "./api/team.js";

export type AccessMode = "choose" | "owner";
export type AccessPreset = "private" | "team" | "borrow" | "open" | "custom";

export const PRESETS: { id: AccessPreset; name: string; hint: string; soon?: string }[] = [
  { id: "private", name: "Pribadi", hint: "Hanya connector yang dipilih, tiap orang memakai aksesnya sendiri." },
  { id: "team", name: "Tim", hint: "Semua connector milikmu; tiap orang memakai aksesnya sendiri." },
  { id: "borrow", name: "Pinjam", hint: "Yang tanpa akses memakai akunmu, hanya baca." },
  { id: "open", name: "Terbuka", hint: "Siapa saja memakai akun bersama, daftar sempit.", soon: "butuh tier guest" },
  { id: "custom", name: "Kustom", hint: "Atur sendiri per connector di tabel." },
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

const LEVEL_NAME: Record<string, string> = { off: "mati", read: "baca", pick: "pilihan op", all: "semua" };
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
  if (who === "owner") return { level, as: "sebagai kamu" };
  if (g?.lend) return { level, as: who === "mate" ? "aksesnya sendiri, tanpa akses: akunmu" : "akunmu (dipinjam)" };
  return who === "mate" ? { level, as: "aksesnya sendiri" } : { level: "off", as: "tidak ada" };
}

/** accessDiff lists, in words, what saving would change. */
export function accessDiff(
  before: { mode: AccessMode; grants: ConnectorGrant[] },
  after: { mode: AccessMode; grants: ConnectorGrant[] },
  label: (id: string) => string,
): string[] {
  const out: string[] = [];
  if (before.mode !== after.mode) out.push(`Mode akses: ${before.mode === "owner" ? "semua (All)" : "pilihan (Selected)"} → ${after.mode === "owner" ? "semua (All)" : "pilihan (Selected)"}`);
  const b = new Map(before.grants.map((g) => [g.connector_id, g]));
  const a = new Map(after.grants.map((g) => [g.connector_id, g]));
  for (const id of new Set([...b.keys(), ...a.keys()])) {
    const x = b.get(id), y = a.get(id);
    const lx = effectiveLevel(before.mode, x), ly = effectiveLevel(after.mode, y);
    if (lx !== ly) out.push(`${label(id)}: ${levelName(lx)} → ${levelName(ly)}`);
    if (!!x?.lend !== !!y?.lend) out.push(`${label(id)}: ${y?.lend ? "dipinjamkan" : "tidak dipinjamkan lagi"}`);
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
