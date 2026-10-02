import { Effect } from "effect";
import { apiGetE, apiPostE, apiPatchE, apiDeleteE, WickClientLayer, type APIError } from "@wick-fe/common-api";
import type { HttpClient } from "@effect/platform";
import type { AgentFeatures } from "../agentMode.js";

/* The Agents app API (api_personas.go). Every route is scoped to the
   caller as owner server-side, so nothing here passes a user id. */

export type AgentAvatarSpec = { shape: string; color: string };

export type ConnectorGrant = {
  connector_id: string;
  /** Empty = every account the owner sees; "" inside = the instance/bot. */
  accounts: string[];
  level: "all" | "read" | "pick";
  ops: string[];
};

export type AgentItem = {
  id: string;
  handle: string;
  is_captain: boolean;
  project_id: string;
  name: string;
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
  disabled: boolean;
  main_session_id: string;
  last_active: string | null;
  last_preview: string;
  status: string;
  /** Only on PATCH responses: other agents of this owner on the same project. */
  shared_with?: number;
};

export type AgentWrite = Partial<{
  handle: string;
  name: string;
  icon: string;
  description: string;
  system_prompt: string;
  provider: string;
  model: string;
  project_id: string;
  avatar: AgentAvatarSpec;
  features: AgentFeatures;
  allowed_connectors: ConnectorGrant[];
  include_new_connectors: boolean;
  disabled: boolean;
  is_captain: boolean;
}>;

export type PersonaConnector = {
  id: string;
  key: string;
  label: string;
  description: string;
  accounts: { id: string; display_name: string }[] | null;
  ops: { key: string; name: string; destructive: boolean }[] | null;
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
  apiGetE<{ agents: AgentItem[] | null; captain_id: string }>(`${base}/api/personas`);

export const createAgent = (base: string, body: AgentWrite) =>
  apiPostE<AgentItem>(`${base}/api/personas`, body);

export const updateAgent = (base: string, id: string, body: AgentWrite) =>
  apiPatchE<AgentItem>(`${base}/api/personas/${enc(id)}`, body);

export const deleteAgent = (base: string, id: string) =>
  apiDeleteE<unknown>(`${base}/api/personas/${enc(id)}`);

export const listAgentConnectors = (base: string) =>
  apiGetE<PersonaConnector[] | null>(`${base}/api/personas/connectors`);

/** openAgentChat returns the agent's main session (created on first use),
    or a fresh side conversation when fresh=true. */
export const openAgentChat = (base: string, id: string, fresh = false) =>
  apiPostE<{ session_id: string }>(`${base}/api/personas/${enc(id)}/chat`, fresh ? { new: true } : {});

export const listAgentSessions = (base: string, id: string) =>
  apiGetE<AgentSessionItem[] | null>(`${base}/api/personas/${enc(id)}/sessions`);

/** runApi runs one of the effects above as a promise — the Agents app
    components only ever need the result or the error message. */
export const runApi = <T>(eff: Effect.Effect<T, APIError, HttpClient.HttpClient>): Promise<T> =>
  Effect.runPromise(eff.pipe(Effect.provide(WickClientLayer)));

/** isWorking reads a session status as "the agent is busy right now". */
export const isWorking = (status: string | undefined) => status === "running" || status === "queued";
