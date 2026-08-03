import { apiDelete, apiGet, apiPost } from "./client.js";
import type { ProviderListItem } from "./providerList.js";

/**
 * AgentProfile is one sub-agent role.
 *
 * `project_id` is the scope: empty means global (reachable from every
 * project), non-empty means the role belongs to that project and is
 * invisible elsewhere. A project role whose `key` matches a global one
 * shadows it for sessions in that project.
 */
export interface AgentProfile {
  id: string;
  project_id: string;
  key: string;
  name: string;
  description: string;
  icon: string;
  provider: string;
  model: string;
  system_prompt: string;
  allowed_tag_ids: string[];
  allowed_native_tools: string[];
  strict_mcp: boolean;
  default_max_turns: number;
  can_delegate: boolean;
  allow_take_over: boolean;
  disabled: boolean;
  /** Frozen: no edit and no delete is accepted while true. Only the web
      UI can clear it — MCP may set it, never unset it. */
  locked: boolean;
}

export interface AgentProfileList {
  /** What a session in this scope resolves to, shadows already applied. */
  profiles: AgentProfile[];
  /**
   * The rows this project owns — empty for the global scope. These are
   * the only ones the project tab may edit or delete.
   */
  owned: AgentProfile[];
  /**
   * The global rows this project inherited, unresolved — empty for the
   * global scope. Returned separately because `profiles` has already
   * dropped any global row a project role shadows, so the merged list
   * alone cannot tell you that an override is an override.
   */
  inherited: AgentProfile[];
  /** Tags the caller holds. allowed_tag_ids only narrows the delegating
      human's own set, so a tag they lack would be inert if offered. */
  tags: TagOption[];
  /** Provider instances a role may run on, with their models — the same
      list the composer and the project defaults picker use. */
  provider_list: ProviderListItem[];
  /** Whether the caller may mutate GLOBAL roles. Project roles are
      governed by project access instead and are not covered by this. */
  is_admin: boolean;
}

export interface TagOption {
  id: string;
  name: string;
}

/** Providers that can act as a leader, i.e. call wick_delegate. Mirrors
 * leaderCapableProviders in internal/tools/agents/api_agent_profiles.go —
 * the server forces can_delegate off for anything else, so the form
 * disables the field rather than letting a saved value vanish. */
export const LEADER_CAPABLE_PROVIDERS = ["claude", "codex", "wick"];

/** Reduces a stored provider value ("wick/wick::cc/claude-fable-5") to its
    bare type. Mirrors providerTypeOf in
    internal/tools/agents/api_agent_profiles.go. */
export function providerTypeOf(v: string): string {
  return v.split("::")[0].split("/")[0];
}

/** Returns the "type/name" form a picker option carries. A bare type —
    what every role stored before instances existed — becomes its canonical
    instance ("claude" → "claude/claude"), so an old row matches a real
    option instead of rendering as "(unavailable)". Mirrors
    normalizeProviderKey in internal/agents/provider/switcher.go. */
export function normalizeProviderKey(key: string): string {
  return key.includes("/") ? key : `${key}/${key}`;
}

export function canLeadDelegation(provider: string): boolean {
  return LEADER_CAPABLE_PROVIDERS.includes(providerTypeOf(provider));
}

export function emptyAgentProfile(projectID = ""): AgentProfile {
  return {
    id: "",
    project_id: projectID,
    key: "",
    name: "",
    description: "",
    icon: "",
    provider: "claude",
    model: "",
    system_prompt: "",
    allowed_tag_ids: [],
    allowed_native_tools: [],
    strict_mcp: true,
    default_max_turns: 12,
    can_delegate: false,
    allow_take_over: false,
    disabled: false,
    locked: false,
  };
}

/**
 * listAgentProfiles fetches the roles visible in a scope.
 *
 * An absent projectID means the global scope. The parameter is omitted
 * rather than sent empty: `?project_id=` would read as "a project whose
 * id is the empty string" on any server that distinguishes the two.
 */
export async function listAgentProfiles(
  base: string,
  projectID?: string,
): Promise<AgentProfileList> {
  const qs = projectID ? `?project_id=${encodeURIComponent(projectID)}` : "";
  const r = await apiGet<Partial<AgentProfileList>>(`${base}/api/agent-profiles${qs}`);
  return {
    profiles: r.profiles ?? [],
    owned: r.owned ?? [],
    inherited: r.inherited ?? [],
    tags: r.tags ?? [],
    provider_list: r.provider_list ?? [],
    is_admin: r.is_admin ?? false,
  };
}

export async function saveAgentProfile(base: string, p: AgentProfile): Promise<AgentProfile> {
  return apiPost<AgentProfile>(`${base}/api/agent-profiles`, p);
}

export async function deleteAgentProfile(base: string, id: string): Promise<void> {
  await apiDelete<unknown>(`${base}/api/agent-profiles/${encodeURIComponent(id)}`);
}

/**
 * shadowedKeys returns the keys this project overrides — the roles it
 * owns under a key the global scope also defines.
 *
 * Intersected on the client from the two unresolved lists rather than
 * read from a server flag, so the "shadows global" label cannot disagree
 * with the rows it is describing.
 */
export function shadowedKeys(list: AgentProfileList): Set<string> {
  const globalKeys = new Set(list.inherited.map((p) => p.key));
  const out = new Set<string>();
  for (const p of list.owned) {
    if (globalKeys.has(p.key)) out.add(p.key);
  }
  return out;
}
