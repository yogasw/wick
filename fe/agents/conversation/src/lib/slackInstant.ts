import { apiDeleteE, apiGetE, apiPostE, apiPutE } from "@wick-fe/common-api";

/* Connections › Slack › Instant (api_team_slack_instant.go): the agent
   answers through a Slack app wick already runs, under its own name and
   photo. The avatar URL is never exposed; the preview renders locally. */
export type SlackInstantApp = { key: string; bot_name?: string; team_name?: string; online: boolean; shared: boolean };
export type CustomizeScope = "ok" | "missing" | "unknown";
export type AgentSlackInstantStatus = {
  enabled: boolean;
  shared_channel: string;
  bound_channels: string[];
  prefix_enabled: boolean;
  username: string;
  avatar_ready: boolean;
  shared_online: boolean;
  customize_scope: CustomizeScope;
  warnings?: string[] | null;
};
export type AgentSlackInstantUpdate = Partial<{ shared_channel: string; bound_channels: string[]; prefix_enabled: boolean }>;

const enc = encodeURIComponent;
const path = (base: string, id: string) => `${base}/api/team/agents/${enc(id)}/slack/instant`;

export const listSlackInstantApps = (base: string) => apiGetE<{ apps: SlackInstantApp[] | null }>(`${base}/api/team/slack/instant/apps`);
export const getAgentSlackInstant = (base: string, id: string) => apiGetE<AgentSlackInstantStatus>(path(base, id));
export const updateAgentSlackInstant = (base: string, id: string, body: AgentSlackInstantUpdate) =>
  apiPutE<AgentSlackInstantStatus>(path(base, id), body);
export const disableAgentSlackInstant = (base: string, id: string) => apiDeleteE<{ status: string }>(path(base, id));
export const rotateAgentSlackInstantAvatar = (base: string, id: string) =>
  apiPostE<AgentSlackInstantStatus>(`${path(base, id)}/rotate-avatar`, {});

const channelIDRe = /^[CG][A-Z0-9]{6,}$/;
const archiveRe = /\/(?:archives|client\/[A-Z0-9]+)\/([CG][A-Z0-9]{6,})/;

/** channelOf pulls a channel id out of what was pasted — an id (with or
    without #) or a channel / message link. "" when it is neither; mirrors
    normalizeSlackChannel on the server. */
export function channelOf(raw: string): string {
  const v = raw.trim();
  const m = archiveRe.exec(v);
  if (m) return m[1];
  const id = v.replace(/^#/, "").toUpperCase();
  return channelIDRe.test(id) && /\d/.test(id) ? id : "";
}

/** appLabel names a shared app in the picker. */
export function appLabel(a: SlackInstantApp): string {
  const who = a.bot_name ? `@${a.bot_name}` : a.key;
  const where = a.team_name ? ` in ${a.team_name}` : "";
  const tag = a.shared ? " (wick shared app)" : "";
  return `${who}${where}${tag}${a.online ? "" : " · offline"}`;
}

/** prefixExample is how someone calls the agent through the shared bot. */
export const prefixExample = (bot: string | undefined, handle: string) => `@${bot || "bot"} ${handle}: summarize this thread`;

/** instantStatusLine is the one-line state under the Slack card title. */
export function instantStatusLine(st: AgentSlackInstantStatus | null): string {
  if (!st?.enabled) return "Not connected";
  const n = st.bound_channels.length;
  const where = n ? `${n} channel${n === 1 ? "" : "s"}` : st.prefix_enabled ? "prefix only" : "no channels yet";
  return `${st.shared_online ? "Instant" : "Instant · shared app offline"} — ${where}`;
}

/** Limits of a persona on a shared app, shown as-is on the card. */
export const INSTANT_LIMITS = [
  "The agent is not a Slack user: people can't DM it directly, and it isn't in the member list.",
  "DMs to the shared bot reach it only with the prefix.",
  "In a channel, mention the shared bot (or reply in a thread it already answers) — plain messages are not picked up.",
  "The name and photo are cosmetic: Slack still shows the message as sent by the shared app.",
];
