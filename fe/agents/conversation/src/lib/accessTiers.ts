/* Access tiers: the server decides an agent's level on a connector as
   explicit grant > tier default > include-new > nothing (team.Scope.Level).
   These helpers mirror that so the Access tab can show "Default (Write)"
   and keep tier defaults out of the stored grants. */
import type { AgentConnector, ConnectorGrant } from "./api/team.js";

export type Tier = "connectors" | "platform" | "system";
export type Override = "default" | "off" | "read" | "all" | "pick";

export const tierOf = (c: AgentConnector): Tier =>
  c.tier === "system" ? "system" : c.tier === "platform" ? "platform" : "connectors";

/** What an entry gets with no grant: Platform write for all, System write for the Captain. */
export function tierDefault(c: AgentConnector, isCaptain: boolean): "all" | "off" {
  const t = tierOf(c);
  if (t === "platform") return "all";
  if (t === "system" && isCaptain) return "all";
  return "off";
}

export function overrideOf(grants: ConnectorGrant[], id: string): Override {
  const g = grants.find((x) => x.connector_id === id);
  return g ? g.level : "default";
}

/** setOverride stores an explicit grant, or drops it for "default". */
export function setOverride(grants: ConnectorGrant[], id: string, o: Override): ConnectorGrant[] {
  const rest = grants.filter((g) => g.connector_id !== id);
  if (o === "default") return rest;
  const prev = grants.find((g) => g.connector_id === id);
  return [...rest, { connector_id: id, accounts: prev?.accounts ?? [], level: o, ops: o === "pick" ? (prev?.ops ?? []) : [] }];
}

/** Accounts a grant ticks; [] on the grant means every listed account. */
export function tickedAccounts(g: ConnectorGrant | undefined, all: string[]): Set<string> {
  if (!g) return new Set();
  return new Set(g.accounts.length === 0 ? all : g.accounts.filter((a) => all.includes(a)));
}

/** toggleAccount ticks one account independently. Every account ticked
    stores [] ("all"); none ticked drops the grant — no account is locked. */
export function toggleAccount(grants: ConnectorGrant[], id: string, all: string[], acc: string, on: boolean): ConnectorGrant[] {
  const g = grants.find((x) => x.connector_id === id);
  const set = tickedAccounts(g, all);
  if (on) set.add(acc);
  else set.delete(acc);
  const rest = grants.filter((x) => x.connector_id !== id);
  if (set.size === 0) return rest;
  const accounts = set.size === all.length ? [] : all.filter((a) => set.has(a));
  return [...rest, { connector_id: id, accounts, level: g?.level ?? "read", ops: g?.ops ?? [] }];
}

/** setLevelFor applies one level to many rows at once ("off" on a
    Connectors row clears it; on a tier row it stores an off override). */
export function setLevelFor(grants: ConnectorGrant[], rows: AgentConnector[], level: "read" | "all" | "off"): ConnectorGrant[] {
  let out = grants;
  for (const c of rows) {
    const lv = c.tool && level === "read" ? "all" : level;
    if (lv === "off" && tierOf(c) === "connectors") out = out.filter((g) => g.connector_id !== c.id);
    else out = setOverride(out, c.id, lv);
  }
  return out;
}
