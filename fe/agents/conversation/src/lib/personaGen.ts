import type { AgentConnector } from "./api/team.js";

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
  /** Suggested connector keys; never granted on their own. */
  connectors: string[];
};

export type PersonaFields = { name?: string; tagline?: string; description?: string; system_prompt?: string };

/** personaInput is the job input: the brief plus whatever the form holds,
    so Improve keeps the intent and suggestions name real connectors. */
export function personaInput(target: PersonaTarget, brief: string, cur: PersonaFields, catalog: AgentConnector[]) {
  const fields: Record<string, string> = { target };
  for (const [k, v] of Object.entries(cur)) if (v && v.trim()) fields[k] = v;
  const keys = catalog.filter((c) => !c.tier && !c.tool).map((c) => c.key);
  if (keys.length) fields.connectors = keys.join(", ");
  return { text: brief.trim(), fields };
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
