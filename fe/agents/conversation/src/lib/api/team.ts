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
  /** "owner" = Same as me (every connector the owner has, write included);
      older servers omit it, which reads as "choose". */
  access_mode?: "choose" | "owner";
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
  /** "Manage other agents" (agents.* ops), default applied: on for the
      Captain, off for the rest. Older servers omit it. */
  manage_agents?: boolean;
  /** What the Captain may do to this agent. Older servers omit it. */
  captain_can?: CaptainCan;
  /** Tools & features: native tools on, Bash commands that run unasked,
      and whether the provider is held to them. Skills tab: skills off.
      Older servers omit them. */
  allowed_native_tools?: string[];
  bash_rules?: BashRule[];
  native_tools_enforced?: boolean;
  disabled_skills?: string[];
  /** Prompt chips of the agent view (Slack) and an empty chat; max 4. */
  suggested_prompts?: SuggestedPrompt[];
  main_session_id: string;
  last_active: string | null;
  last_preview: string;
  status: string;
  /** Main chat moved since the owner last opened it (markAgentRead). */
  unread?: boolean;
  /** How many messages arrived unread, for the roster's green pill; omitted
      by servers that only flag unread. */
  unread_count?: number;
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
  /** "viewer" = another owner shared this agent with the user: chat and
      info only (agentSharing.ts); absent for the user's own agents. */
  role?: "viewer";
  /** The owner who shared it (role "viewer"). */
  shared_by?: string;
  shared_by_id?: string;
  /** "" = an agent wick runs itself, "a2a-remote" = another system's A2A
      agent (then `remote` holds its card and settings), "slack-remote" =
      an agent reached through Slack (then `slack_remote`). */
  kind?: "" | "a2a-remote" | "slack-remote" | "plugin-remote";
  remote?: RemoteAgentInfo;
  slack_remote?: SlackRemoteInfo;
};

/* A2A remote agents (api_team_a2a_remote.go). The auth secret goes out
   on writes only; no response ever carries it back (auth_set). */

export type RemoteAuthType = "none" | "bearer" | "api_key";
export type RemoteAuthReq = { type: RemoteAuthType; header?: string; secret?: string };
/** Legacy "Who may use it"; "mention" = the Mention tab decides (read only). */
export type RemoteUsage = "only_me" | "me_and_my_agents" | "mention";

export type RemoteSkill = { id: string; name: string; description?: string; examples?: string[] };

export type RemoteCard = {
  name: string;
  description: string;
  version: string;
  streaming: boolean;
  icon_url?: string;
  /** card.provider.organization */
  provider?: string;
  skills: RemoteSkill[] | null;
  endpoint: string;
  transport: string;
};

/** Per chat, only on GET …/a2a-remote?session_id=. */
export type RemoteState = {
  context_id?: string;
  task_id?: string;
  /** The remote waits on an answer: the next message continues its task. */
  input_required?: boolean;
  last_state?: string;
  updated_at: string;
};

export type RemoteAgentInfo = {
  card_url: string;
  host: string;
  card: RemoteCard;
  auth_type: RemoteAuthType;
  auth_header?: string;
  auth_set: boolean;
  timeout_sec: number;
  max_response_bytes: number;
  usage: RemoteUsage;
  refreshed_at: string;
  session?: RemoteState;
};

export type RemoteResolved = { card_url: string; host: string; card: RemoteCard; suggested_handle: string };

/** Always 200: ok false carries the failing step in state and error. */
export type RemoteTestResult = { ok: boolean; state: string; card_ms: number; latency_ms: number; reply: string; error: string };

export type RemoteCreate = {
  url: string;
  auth?: RemoteAuthReq;
  handle?: string;
  tagline?: string;
  avatar?: AgentAvatarSpec;
  timeout_sec?: number;
  max_response_bytes?: number;
  usage?: RemoteUsage;
};

export type RemoteUpdate = Partial<{ timeout_sec: number; max_response_bytes: number; usage: RemoteUsage; auth: RemoteAuthReq }>;

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
  access_mode: "choose" | "owner";
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
  manage_agents: boolean;
  captain_can: CaptainCan;
  allowed_native_tools: string[];
  bash_rules: BashRule[];
  disabled_skills: string[];
  suggested_prompts: SuggestedPrompt[];
}>;

/** team.BashRule: a command glob and where its path arguments may point
    ("{project}", an absolute path, or "" = the project folder). */
export type BashRule = { pattern: string; scope: string };

/** One skill the agent's spawns see (GET …/skills). */
export type AgentSkill = {
  name: string;
  description: string;
  source: "local" | "global" | "builtin";
  /** A local skill hiding a global/built-in one of the same name. */
  overrides?: "global" | "builtin";
  /** The hidden entry ("local wins"). */
  shadowed?: boolean;
  /** Cannot be switched off. */
  required?: boolean;
  disabled: boolean;
};

/** team.CaptainCan: what the Captain may do to one agent. Access is only
    ever a proposal the owner approves, even when on. */
export type CaptainCan = { persona: boolean; access: boolean; routines: boolean };

/** One row of Settings › Access › History. */
export type AccessHistoryItem = {
  id: string;
  /** A person's name, or "@handle" for an agent. */
  actor: string;
  status: "applied" | "declined" | "pending" | string;
  diff: string[];
  decided_by?: string;
  at: string;
};

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

export const resolveRemoteCard = (base: string, url: string, auth?: RemoteAuthReq) =>
  apiPostE<RemoteResolved>(`${base}/api/team/a2a-remote/resolve`, auth ? { url, auth } : { url });

/** By url (wizard) or agent_id (Settings; no auth = the stored one). */
export const testRemoteAgent = (base: string, body: { url?: string; agent_id?: string; auth?: RemoteAuthReq }) =>
  apiPostE<RemoteTestResult>(`${base}/api/team/a2a-remote/test`, body);

export const createRemoteAgent = (base: string, body: RemoteCreate) =>
  apiPostE<AgentItem>(`${base}/api/team/a2a-remote`, body);

export const getRemoteAgent = (base: string, id: string, sessionId?: string) =>
  apiGetE<RemoteAgentInfo>(`${base}/api/team/agents/${enc(id)}/a2a-remote${sessionId ? `?session_id=${enc(sessionId)}` : ""}`);

export const updateRemoteAgent = (base: string, id: string, body: RemoteUpdate) =>
  apiPatchE<RemoteAgentInfo>(`${base}/api/team/agents/${enc(id)}/a2a-remote`, body);

/** Re-reads the card: skills, version and streaming change; handle and avatar stay. */
export const refreshRemoteCard = (base: string, id: string) =>
  apiPostE<RemoteAgentInfo>(`${base}/api/team/agents/${enc(id)}/a2a-remote/refresh-card`, {});

/* Slack remote agents (api_team_slack_remote.go): wick posts the turn to
   a DM, channel or thread and reads the reply back. No token is ever in a
   response; identity "user" needs an account the caller connected. */

export type SlackIdentity = "bot" | "user";
export type SlackTarget = "dm" | "channel" | "thread";
export type SlackListen = "target" | "anyone";

export type SlackRemoteConfig = {
  connector_id: string;
  identity: SlackIdentity;
  account_id?: string;
  target: SlackTarget;
  channel?: string;
  /** DM: the user or bot user id. */
  user?: string;
  /** Channel: who is @-mentioned when a chat opens its thread. */
  mention_id?: string;
  thread_ts?: string;
  /** UI label ("#ops", "@helper"). */
  target_name?: string;
  listen: SlackListen;
  /** END RESPONSE marker; unset = on. */
  marker?: boolean;
  /** Start every turn with @target; unset = on. */
  mention_target?: boolean;
  /** 0 = the server default (20 s / 180 s), at most 900. */
  idle_sec?: number;
  max_sec?: number;
  /** Longest wait between two thread reads; 0 = the default backoff. */
  poll_sec?: number;
  /** Seconds late replies are still passed on; 0 = default, -1 = off. */
  grace_sec?: number;
  usage?: RemoteUsage;
};

export type SlackRemoteInfo = SlackRemoteConfig & {
  updated_at: string;
  marker: boolean;
  mention_target: boolean;
  idle_sec_effective: number;
  max_sec_effective: number;
  listen_effective: string;
  usage_effective: string;
  warning: string;
  session?: { channel?: string; thread_ts?: string; last_ts?: string; updated_at: string };
};

export type SlackTestState = "replied" | "no_reply" | "send_failed" | "auth_failed";
export type SlackTestResult = { ok: boolean; state: SlackTestState | string; latency_ms: number; reply?: string; error?: string };

export type SlackIdentities = { bot: boolean; accounts: { id: string; display_name: string }[] | null };

export type SlackRemoteCreate = SlackRemoteConfig & { name?: string; handle?: string; tagline?: string };

/** Only the caller's own OAuth accounts on that connector. */
export const getSlackIdentities = (base: string, connectorId: string) =>
  apiGetE<SlackIdentities>(`${base}/api/team/slack-remote/identities?connector_id=${enc(connectorId)}`);

/** One pickable Slack user, bot or channel from the target search. */
export type SlackDirEntry = {
  id: string;
  name: string;
  real_name?: string;
  display_name?: string;
  avatar?: string;
  is_bot?: boolean;
  is_private?: boolean;
};
export type SlackDirectory = { entries: SlackDirEntry[] | null; error?: string; missing_scope?: boolean };

/** Users+bots or channels whose names contain q, for a person to pick
    from; the server caches each workspace listing for a few minutes. */
export const searchSlackDirectory = (
  base: string,
  p: { connectorId: string; identity: SlackIdentity; accountId: string; kind: "users" | "channels"; q: string },
) =>
  apiGetE<SlackDirectory>(
    `${base}/api/team/slack-remote/directory?connector_id=${enc(p.connectorId)}&identity=${enc(p.identity)}&account_id=${enc(p.accountId)}&kind=${p.kind}&q=${enc(p.q)}`,
  );

/** By config (wizard) or agent_id (Settings). Posts a real "ping". */
export const testSlackRemote = (base: string, body: SlackRemoteConfig | { agent_id: string }) =>
  apiPostE<SlackTestResult>(`${base}/api/team/slack-remote/test`, body);

export const createSlackRemote = (base: string, body: SlackRemoteCreate) =>
  apiPostE<AgentItem>(`${base}/api/team/slack-remote`, body);

export const getSlackRemote = (base: string, id: string) =>
  apiGetE<SlackRemoteInfo>(`${base}/api/team/agents/${enc(id)}/slack-remote`);

/** Sent fields overwrite, the rest stay. */
export const updateSlackRemote = (base: string, id: string, body: Partial<SlackRemoteConfig>) =>
  apiPatchE<SlackRemoteInfo>(`${base}/api/team/agents/${enc(id)}/slack-remote`, body);

export const listAgents = (base: string) =>
  apiGetE<{ agents: AgentItem[] | null; captain_id: string }>(`${base}/api/team/agents`);

/** The read-only roster (?ensure=0): never creates the Captain, so a chat
    reading it for its `@` menu cannot trigger Team-app side effects. */
export const listAgentRoster = (base: string) =>
  apiGetE<{ agents: AgentItem[] | null; captain_id: string }>(`${base}/api/team/agents?ensure=0`);

export const createAgent = (base: string, body: AgentWrite) =>
  apiPostE<AgentItem>(`${base}/api/team/agents`, body);

/** getAgent is one agent as the Settings drawer edits it: features
    resolved against the owner's connector catalog and old access switches
    migrated. The roster skips that work, so its features are the stored
    switches. */
export const getAgent = (base: string, id: string) =>
  apiGetE<AgentItem>(`${base}/api/team/agents/${enc(id)}`);

export const updateAgent = (base: string, id: string, body: AgentWrite) =>
  apiPatchE<AgentItem>(`${base}/api/team/agents/${enc(id)}`, body);

/** The agent's latest 20 access changes, newest first. */
export const getAccessHistory = (base: string, id: string) =>
  apiGetE<{ items: AccessHistoryItem[] | null }>(`${base}/api/team/agents/${enc(id)}/access-history`);

/** The skills the agent's spawns see: project-local, global, built-in. */
export const getAgentSkills = (base: string, id: string) =>
  apiGetE<{ items: AgentSkill[] | null; local_dir: string }>(`${base}/api/team/agents/${enc(id)}/skills`);

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

/** setAgentMainChat makes sessionId the agent's main chat — where @mentions,
    schedules to "Main chat" and opening the agent land. The old main stays
    as an ordinary chat. */
export const setAgentMainChat = (base: string, id: string, sessionId: string) =>
  apiPostE<{ session_id: string }>(`${base}/api/team/agents/${enc(id)}/main`, { session_id: sessionId });

/** The editable Team settings, one key per entry of the server's
    team.SettingFields. A new setting is added here, to TEAM_SETTING_KEYS,
    and to the tab that shows it (teamSettingsTabs.ts). */
export type TeamSettingValues = {
  /** Team instructions: markdown for every agent in the caller's Team. */
  prompt: string;
  /** "Open Team when I open Agents". */
  open_team: boolean;
  /** "Idle animations": idle avatars fidget now and then. */
  idle_animations: boolean;
};
export const TEAM_SETTING_KEYS: (keyof TeamSettingValues)[] = ["prompt", "open_team", "idle_animations"];

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

/* ── Group chats (team_group.go) ─────────────────────────────────── */

export type GroupMember = {
  id: string;
  handle: string;
  name: string;
  avatar: AgentAvatarSpec;
  is_captain: boolean;
  disabled: boolean;
  max_hops: number;
};

export type GroupItem = {
  id: string;
  name: string;
  members: GroupMember[];
  default_responder: "captain" | "first";
  /** Handle a message with no @ goes to now. */
  responder: string;
  /** 0 = none; only ever lower than members_max_hops. */
  max_hops_override: number;
  members_max_hops: number;
  /** The cap in force. */
  max_hops: number;
  last_active: string | null;
  last_preview: string;
  unread: boolean;
  /** See AgentItem.unread_count. */
  unread_count?: number;
};

export type GroupWrite = Partial<{
  name: string;
  members: string[];
  default_responder: "captain" | "first";
  max_hops_override: number;
}>;

export type GroupTurn = {
  turn_id?: string;
  ts: string;
  role: "user" | "assistant" | "system";
  text: string;
  kind?: string;
  extras?: Record<string, string>;
  is_error?: boolean;
  sender?: { name?: string } | null;
  /** session_id: the member's backing session the turn ran in (via "group"). */
  speaker?: { agent_id: string; handle?: string; via: string; session_id?: string } | null;
};

export const listGroups = (base: string) => apiGetE<{ groups: GroupItem[] | null }>(`${base}/api/team/groups`);
export const createGroup = (base: string, body: GroupWrite) => apiPostE<GroupItem>(`${base}/api/team/groups`, body);
export const updateGroup = (base: string, id: string, body: GroupWrite) =>
  apiPatchE<GroupItem>(`${base}/api/team/groups/${enc(id)}`, body);
export const deleteGroup = (base: string, id: string) =>
  apiDeleteE<{ status: string }>(`${base}/api/team/groups/${enc(id)}`);
export const markGroupRead = (base: string, id: string) =>
  apiPostE<{ status: string }>(`${base}/api/team/groups/${enc(id)}/read`, {});
export const groupConversation = (base: string, id: string) =>
  apiGetE<{ turns: GroupTurn[] | null }>(`${base}/api/sessions/${enc(id)}/conversation?limit=200`);
export const sendToGroup = (base: string, id: string, text: string) =>
  apiPostE<{ status: string }>(`${base}/sessions/${enc(id)}/send`, { text });

/** runApi runs one of the effects above as a promise — the Agents app
    components only ever need the result or the error message. */
export const runApi = <T>(eff: Effect.Effect<T, APIError, HttpClient.HttpClient>): Promise<T> =>
  Effect.runPromise(eff.pipe(Effect.provide(WickClientLayer)));

/** isWorking reads a session status as "the agent is busy right now". */
export const isWorking = (status: string | undefined) => status === "running" || status === "queued";

/** Sends text into a chat as the user (the "Create skill from this chat"
    button posts its instruction to the agent's main chat). */
export const sendToChat = (base: string, sessionID: string, text: string) =>
  apiPostE<{ status: string }>(`${base}/sessions/${enc(sessionID)}/send`, { text });

/** team.SuggestedPrompt: the chip's title and what clicking it sends. */
export type SuggestedPrompt = { title: string; message: string };

/* Connections › Slack (api_team_slack.go). Secrets never come back: the
   server answers whether each one is set, nothing more. */
export type SlackSecretKey = "bot_token" | "app_token" | "signing_secret" | "app_config_token";
export type AgentSlackStatus = {
  connected: boolean;
  online: boolean;
  mode: "socket" | "http";
  bot_id?: string;
  bot_name?: string;
  team_name?: string;
  dm_main_chat: boolean;
  app_id?: string;
  secrets: Partial<Record<SlackSecretKey, boolean>>;
  disabled: boolean;
};
export type AgentSlackConnect = Partial<{
  mode: "socket" | "http";
  bot_token: string;
  app_token: string;
  signing_secret: string;
  app_config_token: string;
  app_id: string;
  dm_main_chat: boolean;
}>;
export type SlackMatrixItem = { name: string; status: SlackMatrixStatus; hint?: string };
export type SlackMatrixStatus = "ok" | "warn" | "error" | "off" | "pending";
export type SlackMatrixRow = {
  key: string; label: string; need: string; status: SlackMatrixStatus;
  /** Where the event verdicts come from. */
  events_from?: "manifest" | "received";
  scopes: SlackMatrixItem[]; events: SlackMatrixItem[];
};
export type SlackHealthCheck = { name: string; ok: boolean; error?: string; detail?: string };
export type AgentSlackHealth = { checks: SlackHealthCheck[] | null; matrix: SlackMatrixRow[] | null };

export const getAgentSlack = (base: string, id: string) =>
  apiGetE<AgentSlackStatus>(`${base}/api/team/agents/${enc(id)}/slack`);
export const connectAgentSlack = (base: string, id: string, body: AgentSlackConnect) =>
  apiPutE<AgentSlackStatus>(`${base}/api/team/agents/${enc(id)}/slack`, body);
export const updateAgentSlack = (base: string, id: string, body: AgentSlackConnect) =>
  apiPatchE<AgentSlackStatus>(`${base}/api/team/agents/${enc(id)}/slack`, body);
export const disconnectAgentSlack = (base: string, id: string) =>
  apiDeleteE<unknown>(`${base}/api/team/agents/${enc(id)}/slack`);
export const getAgentSlackHealth = (base: string, id: string) =>
  apiGetE<AgentSlackHealth>(`${base}/api/team/agents/${enc(id)}/slack/health`);
export const getAgentSlackManifest = (base: string, id: string) =>
  apiGetE<{ manifest: unknown; create_url: string }>(`${base}/api/team/agents/${enc(id)}/slack/manifest`);

/** One row of an agent's Scheduled drawer (GET /api/team/agents/{id}/scheduled). */
export type AgentSchedule = {
  id: string;
  title: string;
  message: string;
  kind: "once" | "recurring";
  status: string;
  paused?: boolean;
  held_by_agent?: boolean;
  interval_ms?: number;
  cron?: string;
  cron_timezone?: string;
  next_run_at?: string;
  last_run_at?: string;
  last_error?: string;
  run_count: number;
  destination: "main" | "telegram" | "slack" | "chat" | "new_chat";
  session_id: string;
  /** The Telegram chat a "telegram" destination posts into. */
  telegram_session?: string;
  /** The Slack channel a "slack" destination's thread is in. */
  slack_channel?: string;
};
/** A chat of the agent's Telegram bot a schedule can post into. */
export type AgentTelegramChat = { session_id: string; title: string };
/** A Slack channel the agent's threads already live in (or, Instant, is bound to). */
export type AgentSlackChannel = { id: string };
export type AgentScheduledList = {
  items: AgentSchedule[];
  feature_on: boolean;
  agent_disabled: boolean;
  server_timezone: string;
  main_session_id: string;
  slack_online: boolean;
  /** The Telegram destination exists only while the bot is connected. */
  telegram_connected?: boolean;
  telegram_chats?: AgentTelegramChat[];
  /** The Slack destination exists only while the agent's Slack connection
      (its own app, or Instant on the shared app) can post. */
  slack_ready?: boolean;
  slack_mode?: "custom" | "instant" | "";
  slack_channels?: AgentSlackChannel[];
};
/** Create/edit body: exactly one of run_at / every / cron. */
export type AgentScheduleWrite = {
  message?: string;
  run_at?: string;
  every?: string;
  cron?: string;
  destination?: "main" | "telegram" | "slack";
  telegram_session?: string;
  slack_channel?: string;
};
/** One fire of a schedule (GET …/scheduled/{sid}/runs), newest first. */
export type AgentScheduleRun = {
  at: string;
  session_id: string;
  turn_id: string;
  status: "ok" | "failed" | "running";
  error?: string;
};
export type AgentScheduleRuns = { items: AgentScheduleRun[]; last_error?: string };

export const getAgentScheduled = (base: string, id: string) =>
  apiGetE<AgentScheduledList>(`${base}/api/team/agents/${enc(id)}/scheduled`);
export const createAgentSchedule = (base: string, id: string, body: AgentScheduleWrite) =>
  apiPostE<AgentSchedule>(`${base}/api/team/agents/${enc(id)}/scheduled`, body);
export const updateAgentSchedule = (base: string, id: string, sid: string, body: AgentScheduleWrite) =>
  apiPatchE<AgentSchedule>(`${base}/api/team/agents/${enc(id)}/scheduled/${enc(sid)}`, body);
export const deleteAgentSchedule = (base: string, id: string, sid: string) =>
  apiDeleteE<unknown>(`${base}/api/team/agents/${enc(id)}/scheduled/${enc(sid)}`);
export const getAgentScheduleRuns = (base: string, id: string, sid: string) =>
  apiGetE<AgentScheduleRuns>(`${base}/api/team/agents/${enc(id)}/scheduled/${enc(sid)}/runs`);
export const agentScheduleAction = (base: string, id: string, sid: string, action: "pause" | "resume" | "run") =>
  apiPostE<AgentSchedule>(`${base}/api/team/agents/${enc(id)}/scheduled/${enc(sid)}/${action}`, {});

/** Settings › Session (GET/PATCH /api/team/agents/{id}/session). */
export type AgentSessionPolicy = {
  compact: "auto" | "idle";
  idle_hours: number;
  summarise_threads: boolean;
  main_session_id: string;
  slack_dm_main_chat: boolean;
  slack_connected: boolean;
};
export const getAgentSession = (base: string, id: string) =>
  apiGetE<AgentSessionPolicy>(`${base}/api/team/agents/${enc(id)}/session`);
export const updateAgentSession = (base: string, id: string, body: Partial<Pick<AgentSessionPolicy, "compact" | "idle_hours" | "summarise_threads">>) =>
  apiPatchE<AgentSessionPolicy>(`${base}/api/team/agents/${enc(id)}/session`, body);
export const compactAgentMain = (base: string, id: string) =>
  apiPostE<{ status: string; session_id: string }>(`${base}/api/team/agents/${enc(id)}/compact`, {});

/* ── Sharing (chat only) ─────────────────────────────────────────────── */

export type AgentShare = { user_id: string; name: string; created_at: string };
export type AgentShares = { shares: AgentShare[]; shareable: boolean; reason: string };
export type ShareUser = { id: string; name: string };

export const listAgentShares = (base: string, id: string) =>
  apiGetE<AgentShares>(`${base}/api/team/agents/${enc(id)}/shares`);

export const addAgentShare = (base: string, id: string, userId: string) =>
  apiPostE<{ status: string }>(`${base}/api/team/agents/${enc(id)}/shares`, { user_id: userId });

export const removeAgentShare = (base: string, id: string, userId: string) =>
  apiDeleteE<{ status: string }>(`${base}/api/team/agents/${enc(id)}/shares/${enc(userId)}`);

/** listShareUsers is who an agent may be shared with: approved wick users,
    the caller aside; ids and names only. */
export const listShareUsers = (base: string) =>
  apiGetE<{ users: ShareUser[] }>(`${base}/api/team/share-users`);

/* ── Plugin remote agents (service plugins with remote_source) ── */

export type PluginSource = { key: string; name: string; description?: string; version: string; state: string };

export type PluginRemoteCreate = { plugin_key: string; name?: string; handle?: string; tagline?: string };

export const listPluginSources = (base: string) => apiGetE<PluginSource[]>(`${base}/api/team/plugin-sources`);

export const createPluginRemote = (base: string, body: PluginRemoteCreate) =>
  apiPostE<AgentItem>(`${base}/api/team/plugin-remote`, body);
