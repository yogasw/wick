import { Effect } from "effect";
import { apiGetE, apiPostE, apiPatchE, apiPutE, apiDeleteE, WickClientLayer, type APIError } from "@wick-fe/common-api";
import type { HttpClient } from "@effect/platform";
import type { AgentFeatures } from "../agentMode.js";

/* The Agents app API (api_team.go). Every route is scoped to the
   caller as owner server-side, so nothing here passes a user id. */

/** team.Avatar: kind "" = classic, "blob" = blob mascot (+ expression). */
export type AgentAvatarSpec = { kind?: string; shape: string; color: string; expression?: string };

export type ConnectorGrant = {
  connector_id: string;
  /** Empty = every account the owner sees; "" inside = the instance/bot. */
  accounts: string[];
  /** "off" overrides a Platform/System tier default downwards. */
  level: "all" | "read" | "pick" | "off";
  ops: string[];
};

export type AgentItem = {
  id: string;
  handle: string;
  is_captain: boolean;
  project_id: string;
  name: string;
  /** Short label people know the agent by ("The Critic"); "" = none. */
  tagline?: string;
  icon: string;
  description: string;
  system_prompt: string;
  provider: string;
  model: string;
  preset: string;
  features: AgentFeatures;
  avatar: AgentAvatarSpec;
  allowed_connectors: ConnectorGrant[] | null;
  include_new_connectors: boolean;
  /** Whose access a turn runs with; rows older than the field read "caller". */
  run_as?: "caller" | "owner";
  disabled: boolean;
  /** Mention tab: who may hand this agent a turn ("all" | "captain" |
      "list" | "off"), the allow list for "list", and its agent-to-agent
      turn cap (1–10, default applied). Older servers omit them. */
  mention_from?: MentionFrom;
  mention_allow?: string[];
  max_hops?: number;
  /** Spawns carry the global system prompt instead of the Team one
      (on for agents converted from a project). */
  use_global_prompt?: boolean;
  /** Whether the chat may pick another provider/model (server applies
      the default: Captain on, others off). */
  allow_provider_switch?: boolean;
  main_session_id: string;
  last_active: string | null;
  last_preview: string;
  status: string;
  /** Main chat moved since the owner last opened it (markAgentRead). */
  unread?: boolean;
  /** Main chat waits on the owner: an ask_user question or an approval. */
  needs_attention?: boolean;
  /** What needs_attention waits on, e.g. "Butuh input: …"; shown in amber
      ahead of last_preview. */
  attention_preview?: string;
  /** Tool the running turn is on ("Bash", "query_range"); "" otherwise. */
  current_action?: string;
  /** Everything else on the same project: other agents (any owner) and
      web/channel conversations. */
  shared_with?: number;
};

export type AgentWrite = Partial<{
  handle: string;
  name: string;
  tagline: string;
  icon: string;
  description: string;
  system_prompt: string;
  provider: string;
  model: string;
  /** Create only: the new project's preset. */
  preset: string;
  project_id: string;
  avatar: AgentAvatarSpec;
  features: AgentFeatures;
  allowed_connectors: ConnectorGrant[];
  include_new_connectors: boolean;
  run_as: "caller" | "owner";
  disabled: boolean;
  allow_provider_switch: boolean;
  is_captain: boolean;
  /** Create only, with project_id: make that project this agent's own. */
  convert: boolean;
  use_global_prompt: boolean;
  mention_from: MentionFrom;
  mention_allow: string[];
  max_hops: number;
}>;

export type MentionFrom = "all" | "captain" | "list" | "off";

export type AgentConnector = {
  id: string;
  key: string;
  label: string;
  description: string;
  accounts: { id: string; display_name: string }[] | null;
  ops: { key: string; name: string; destructive: boolean }[] | null;
  /** Access list: "platform", "system" or "" (Connectors). */
  tier?: "platform" | "system" | "";
  /** A wick MCP tool entry (id "tool:<name>"): on/off only. */
  tool?: boolean;
};

export type AgentSessionItem = {
  id: string;
  label: string;
  last_active: string | null;
  agent_main: boolean;
  status: string;
};

const enc = encodeURIComponent;

export const listAgents = (base: string) =>
  apiGetE<{ agents: AgentItem[] | null; captain_id: string }>(`${base}/api/team/agents`);

/** The read-only roster (?ensure=0): never creates the Captain, so a chat
    reading it for its `@` menu cannot trigger Team-app side effects. */
export const listAgentRoster = (base: string) =>
  apiGetE<{ agents: AgentItem[] | null; captain_id: string }>(`${base}/api/team/agents?ensure=0`);

export const createAgent = (base: string, body: AgentWrite) =>
  apiPostE<AgentItem>(`${base}/api/team/agents`, body);

export const updateAgent = (base: string, id: string, body: AgentWrite) =>
  apiPatchE<AgentItem>(`${base}/api/team/agents/${enc(id)}`, body);

/** chats="delete" takes the agent's own project with it (chats, files,
    memory); "keep" leaves it as an ordinary project in the sidebar. */
export const deleteAgent = (base: string, id: string, chats: "delete" | "keep") =>
  apiDeleteE<unknown>(`${base}/api/team/agents/${enc(id)}?chats=${chats}`);

/** The persona half an agent reads from its project (same fields
    teamAgentToItem fills from project meta), for the Settings project
    switch and the wizard's "existing project" pick. */
export type ProjectPersona = {
  name: string; description: string; system_prompt: string; provider: string; model: string;
};

export const getProjectPersona = (base: string, projectId: string) =>
  apiGetE<{ name?: string; description?: string; system_addon?: string; default_provider?: string; default_model?: string }>(
    `${base}/api/projects/${enc(projectId)}`,
  ).pipe(
    Effect.map((r): ProjectPersona => ({
      name: r.name ?? "", description: r.description ?? "", system_prompt: r.system_addon ?? "",
      provider: r.default_provider ?? "", model: r.default_model ?? "",
    })),
  );

export const listAgentConnectors = (base: string) =>
  apiGetE<AgentConnector[] | null>(`${base}/api/team/agents/connectors`);

/** openAgentChat returns the agent's main session (created on first use),
    or a fresh side conversation when fresh=true. */
export const openAgentChat = (base: string, id: string, fresh = false) =>
  apiPostE<{ session_id: string }>(`${base}/api/team/agents/${enc(id)}/chat`, fresh ? { new: true } : {});

/** markAgentRead clears the unread mark: the owner opened the chat. */
export const markAgentRead = (base: string, id: string) =>
  apiPostE<{ status: string; last_read_at: string }>(`${base}/api/team/agents/${enc(id)}/read`, {});

export const listAgentSessions = (base: string, id: string) =>
  apiGetE<AgentSessionItem[] | null>(`${base}/api/team/agents/${enc(id)}/sessions`);

/** The editable Team settings, one key per entry of the server's
    team.SettingFields. A new setting is added here, to TEAM_SETTING_KEYS,
    and to the tab that shows it (teamSettingsTabs.ts). */
export type TeamSettingValues = {
  /** Team instructions: markdown for every agent in the caller's Team. */
  prompt: string;
  /** "Open Team when I open Agents". */
  open_team: boolean;
};
export const TEAM_SETTING_KEYS: (keyof TeamSettingValues)[] = ["prompt", "open_team"];

/** The caller's own Team settings (GET/PUT /api/team/settings): the
    values plus read-only hints for drawing them. */
export type TeamSettings = TeamSettingValues & {
  max_prompt_bytes: number;
  /** Admins only: the page that edits the operator prompt of all users. */
  operator_prompt_href?: string;
};

/** A PUT names any subset; the server refuses unknown keys. */
export type TeamSettingsWrite = Partial<TeamSettingValues>;

export const getTeamSettings = (base: string) => apiGetE<TeamSettings>(`${base}/api/team/settings`);

export const saveTeamSettings = (base: string, body: TeamSettingsWrite) =>
  apiPutE<TeamSettings>(`${base}/api/team/settings`, body);

/** runApi runs one of the effects above as a promise — the Agents app
    components only ever need the result or the error message. */
export const runApi = <T>(eff: Effect.Effect<T, APIError, HttpClient.HttpClient>): Promise<T> =>
  Effect.runPromise(eff.pipe(Effect.provide(WickClientLayer)));

/** isWorking reads a session status as "the agent is busy right now". */
export const isWorking = (status: string | undefined) => status === "running" || status === "queued";
