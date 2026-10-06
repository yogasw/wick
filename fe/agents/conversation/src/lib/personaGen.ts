import type { AgentConnector, AgentItem, ConnectorGrant, MentionFrom } from "./api/team.js";
import { setOverride } from "./accessTiers.js";

/* Pure helpers behind the Team app's ✨ buttons (aigen kind
   "agent-persona", see internal/agents/team/generate.go). */

export const PERSONA_KIND = "agent-persona";

export type PersonaTarget = "all" | "system_prompt" | "description" | "improve";

/** The fields Settings' ✨ panel can update, in form order. */
export const PERSONA_UPDATE_FIELDS = [
  { key: "name", label: "Name" },
  { key: "tagline", label: "Tagline" },
  { key: "description", label: "Description" },
  { key: "system_prompt", label: "System prompt" },
] as const;
export type PersonaUpdateField = (typeof PERSONA_UPDATE_FIELDS)[number]["key"];

/** improveInput is personaInput for the "improve" target: the user's
    instruction, the current fields and the ones to rewrite. */
export function improveInput(instruction: string, cur: PersonaFields, update: PersonaUpdateField[], catalog: AgentConnector[]) {
  const inp = personaInput("improve", instruction, cur, catalog);
  inp.fields.update = update.join(",");
  return inp;
}

/** The job result, as the server shapes it. */
export type PersonaDraft = {
  name: string;
  handle: string;
  tagline: string;
  description: string;
  system_prompt: string;
  avatar_shape?: string;
  avatar_color?: string;
  /** Connector keys the agent likely needs; the wizard pre-fills Access
      with them (read-only unless also in write_connectors). */
  connectors: string[];
  /** The subset of connectors it must change things through. */
  write_connectors?: string[];
  /** Suggested Mention policy; absent = leave the form's as it is. */
  mention_from?: MentionFrom;
  /** Agent handles, for mention_from "list". */
  mention_allow?: string[];
};

export type PersonaFields = { name?: string; tagline?: string; description?: string; system_prompt?: string };

/** personaInput is the job input: the brief plus whatever the form holds,
    so Improve keeps the intent and suggestions name real connectors. */
export function personaInput(
  target: PersonaTarget,
  brief: string,
  cur: PersonaFields,
  catalog: AgentConnector[],
  team?: { agents?: Pick<AgentItem, "handle" | "tagline" | "description">[]; captain?: boolean },
) {
  const fields: Record<string, string> = { target };
  for (const [k, v] of Object.entries(cur)) if (v && v.trim()) fields[k] = v;
  const keys = catalog.filter((c) => !c.tier && !c.tool).map((c) => c.key);
  if (keys.length) fields.connectors = keys.join(", ");
  // The teammates, so the draft's mention suggestions name real handles.
  const agents = (team?.agents ?? [])
    .map((a) => `${a.handle}: ${(a.tagline || a.description || "").trim()}`.replace(/: $/, ""))
    .join("\n");
  if (agents) fields.agents = agents;
  if (team?.captain) fields.captain = "true";
  return { text: brief.trim(), fields };
}

/** draftGrants adds a draft's connectors to grants: read, or write for the
    ones in write_connectors. Unknown keys and system/tool rows are skipped,
    and a connector already granted keeps what the owner set. */
export function draftGrants(d: Pick<PersonaDraft, "connectors" | "write_connectors">, catalog: AgentConnector[], grants: ConnectorGrant[]): ConnectorGrant[] {
  let out = grants;
  const write = d.write_connectors ?? [];
  for (const c of suggestedConnectors(d.connectors, catalog, grants.map((g) => g.connector_id))) {
    out = setOverride(out, c.id, write.includes(c.key) || write.includes(c.id) ? "all" : "read");
  }
  return out;
}

/** draftMentionAllow maps a draft's allow handles to agent ids, dropping
    handles that name no agent. */
export function draftMentionAllow(handles: string[] | undefined, agents: Pick<AgentItem, "id" | "handle">[]): string[] {
  const out: string[] = [];
  for (const h of handles ?? []) {
    const a = agents.find((x) => x.handle === h.replace(/^@/, ""));
    if (a && !out.includes(a.id)) out.push(a.id);
  }
  return out;
}

/** acceptsNewAgent reports whether an existing agent will take a turn from
    an agent made now, by that agent's own Mention policy. A new agent is
    on no allow list yet. */
export function acceptsNewAgent(target: Pick<AgentItem, "mention_from">, newIsCaptain: boolean): boolean {
  switch (target.mention_from) {
    case "captain":
      return newIsCaptain;
    case "list":
    case "off":
      return false;
  }
  return true;
}

/** suggestedConnectors maps suggested keys to catalog entries the owner
    can actually add, dropping unknown keys and ones already granted. */
export function suggestedConnectors(keys: string[] | null | undefined, catalog: AgentConnector[], granted: string[]): AgentConnector[] {
  const out: AgentConnector[] = [];
  for (const k of keys ?? []) {
    const c = catalog.find((x) => x.key === k || x.id === k);
    if (c && !c.tier && !c.tool && !granted.includes(c.id) && !out.includes(c)) out.push(c);
  }
  return out;
}
