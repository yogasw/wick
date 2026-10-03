/* Pure helpers behind the Agents app forms (wizard + Settings), kept out of
   the components so they can be unit tested. */
import type { ConnectorGrant, AgentConnector } from "./api/team.js";

/** Same rule the server enforces (persona store): lowercase, digits, "-". */
export const HANDLE_RE = /^[a-z0-9][a-z0-9-]{1,30}$/;

/** slugHandle derives a handle suggestion from a display name. */
export function slugHandle(name: string): string {
  return name
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 31)
    .replace(/-+$/, "");
}

/** uniqueHandle makes a suggested handle free: taken → `<handle>-2`,
    `-3`, … cut short so the suffix still fits the 31-character limit. An
    empty suggestion stays empty (the user has not typed a name yet). */
export function uniqueHandle(handle: string, taken: Iterable<string>): string {
  const used = new Set(taken);
  if (!handle || !used.has(handle)) return handle;
  for (let n = 2; ; n++) {
    const suffix = `-${n}`;
    const next = handle.slice(0, 31 - suffix.length).replace(/-+$/, "") + suffix;
    if (!used.has(next)) return next;
  }
}

/** splitPick unpacks ProviderPicker's "type/name::model" value. */
export function splitPick(v: string): { provider: string; model: string } {
  const i = v.indexOf("::");
  return i < 0 ? { provider: v, model: "" } : { provider: v.slice(0, i), model: v.slice(i + 2) };
}

/** joinPick is splitPick's inverse. */
export function joinPick(provider: string, model: string): string {
  if (!provider) return "";
  return model ? `${provider}::${model}` : provider;
}

/** destructiveAllowed lists "Connector · op" for every destructive op the
    grants let through — what the Akses tab warns about. A connector the
    server no longer lists cannot be judged and is skipped. */
export function destructiveAllowed(grants: ConnectorGrant[], catalog: AgentConnector[]): string[] {
  const out: string[] = [];
  for (const g of grants) {
    const c = catalog.find((x) => x.id === g.connector_id);
    if (!c) continue;
    for (const op of c.ops ?? []) {
      if (!op.destructive) continue;
      const allowed = g.level === "all" || (g.level === "pick" && g.ops.includes(op.key));
      if (allowed) out.push(`${c.label} · ${op.name || op.key}`);
    }
  }
  return out;
}

/** writeConnectors names the connectors whose write ops the grants let
    through, once each — the Owner-mode warning lists them. */
export function writeConnectors(grants: ConnectorGrant[], catalog: AgentConnector[]): string[] {
  const out: string[] = [];
  for (const g of grants) {
    const c = catalog.find((x) => x.id === g.connector_id);
    if (!c) continue;
    const hit = (c.ops ?? []).some(
      (op) => op.destructive && (g.level === "all" || (g.level === "pick" && g.ops.includes(op.key))),
    );
    if (hit) out.push(c.label);
  }
  return out;
}

/* ── Akses checklist ────────────────────────────────────────────── */

/** newGrant is what ticking a connector adds: every visible account and
    read-only ops. Write access is always a second, deliberate step. */
export function newGrant(connectorId: string): ConnectorGrant {
  return { connector_id: connectorId, accounts: [], level: "read", ops: [] };
}

/** opStats feeds a row's "N operasi · M tulis" line. */
export function opStats(c: AgentConnector): { total: number; write: number } {
  const ops = c.ops ?? [];
  return { total: ops.length, write: ops.filter((o) => o.destructive).length };
}

/** checkedCount counts ticked connectors the catalog still lists, against
    the catalog size — the toolbar's "n dari N". */
export function checkedCount(grants: ConnectorGrant[], catalog: AgentConnector[]): { checked: number; total: number } {
  const ids = new Set(catalog.map((c) => c.id));
  return { checked: grants.filter((g) => ids.has(g.connector_id)).length, total: catalog.length };
}

/** selectAll ticks every connector in `visible` that is not ticked yet
    (read-only); ticked ones keep their level. */
export function selectAll(grants: ConnectorGrant[], visible: AgentConnector[]): ConnectorGrant[] {
  const have = new Set(grants.map((g) => g.connector_id));
  return [...grants, ...visible.filter((c) => !have.has(c.id)).map((c) => newGrant(c.id))];
}

/** clearAll unticks every connector in `visible`; ones hidden by the search
    stay as they are. */
export function clearAll(grants: ConnectorGrant[], visible: AgentConnector[]): ConnectorGrant[] {
  const drop = new Set(visible.map((c) => c.id));
  return grants.filter((g) => !drop.has(g.connector_id));
}

/** accountTicked reads a chip: an empty list means every account. */
export function accountTicked(g: ConnectorGrant, id: string): boolean {
  return g.accounts.length === 0 || g.accounts.includes(id);
}

/** toggleAccount flips one account chip. Ticking the last missing one
    collapses back to [] ("every account"), and the final chip cannot be
    unticked — [] would silently mean all of them again. Returns null when
    the change is refused. */
export function toggleAccount(g: ConnectorGrant, all: string[], id: string, on: boolean): string[] | null {
  const cur = g.accounts.length === 0 ? all : g.accounts;
  const next = on ? (cur.includes(id) ? cur : [...cur, id]) : cur.filter((x) => x !== id);
  if (next.length === 0) return null;
  return all.every((x) => next.includes(x)) ? [] : next;
}

/** pruneGrants drops what the catalog no longer offers — a connector, an
    account, a picked op — because the server rejects a list that names
    them. `dropped` says how many items went, for the notice. */
export function pruneGrants(
  grants: ConnectorGrant[],
  catalog: AgentConnector[],
): { grants: ConnectorGrant[]; dropped: number } {
  let dropped = 0;
  const out: ConnectorGrant[] = [];
  for (const g of grants) {
    const c = catalog.find((x) => x.id === g.connector_id);
    if (!c) {
      dropped++;
      continue;
    }
    const accIds = new Set((c.accounts ?? []).map((a) => a.id));
    const opKeys = new Set((c.ops ?? []).map((o) => o.key));
    const accounts = g.accounts.filter((a) => a === "" || accIds.has(a));
    const ops = g.ops.filter((o) => opKeys.has(o));
    dropped += g.accounts.length - accounts.length + g.ops.length - ops.length;
    out.push({ ...g, accounts, ops });
  }
  return { grants: out, dropped };
}

export type GrantErrors = {
  /** Per connector id, one line per rejected item. */
  byConnector: Record<string, string[]>;
  /** Rejected connectors the checklist no longer shows. */
  missing: string[];
  runAs: string;
  /** Anything that is not about one item. */
  general: string;
};

const OUTSIDE = "allowed_connectors: outside your connector access:";

/** parseGrantErrors maps a 400 from PATCH/POST onto the checklist. The
    server lists every rejected item in one message —
    "allowed_connectors: outside your connector access: connector c9,
    account c1/acc-x, op c1/nuke" — so each lands on its own row. */
export function parseGrantErrors(msg: string, catalog: AgentConnector[]): GrantErrors {
  const out: GrantErrors = { byConnector: {}, missing: [], runAs: "", general: "" };
  if (msg.startsWith("run_as")) {
    out.runAs = msg;
    return out;
  }
  const at = msg.indexOf(OUTSIDE);
  if (at < 0) {
    out.general = msg;
    return out;
  }
  const known = new Set(catalog.map((c) => c.id));
  const add = (id: string, line: string) => {
    if (!known.has(id)) {
      if (!out.missing.includes(id)) out.missing.push(id);
      return;
    }
    (out.byConnector[id] ??= []).push(line);
  };
  for (const raw of msg.slice(at + OUTSIDE.length).split(",")) {
    const item = raw.trim();
    const sp = item.indexOf(" ");
    if (sp < 0) continue;
    const kind = item.slice(0, sp);
    const ref = item.slice(sp + 1);
    const slash = ref.indexOf("/");
    const conn = slash < 0 ? ref : ref.slice(0, slash);
    const rest = slash < 0 ? "" : ref.slice(slash + 1);
    if (kind === "connector") add(conn, "connector tidak lagi bisa kamu akses");
    else if (kind === "account") add(conn, `akun ${rest || "bot / instance"} tidak lagi bisa kamu akses`);
    else if (kind === "op") add(conn, `operasi ${rest} tidak aktif atau tidak bisa kamu akses`);
    else out.general = msg;
  }
  return out;
}
