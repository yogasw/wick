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
