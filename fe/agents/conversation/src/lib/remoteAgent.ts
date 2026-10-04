import type { AgentItem, RemoteAuthReq, RemoteAuthType } from "./api/team.js";
import type { RailTab } from "./agentMode.js";
import type { SettingsTab } from "./agentsRouter.js";
import { SLACK_REMOTE_KIND, slackCaption, targetLabel } from "./slackRemote.js";

/** remoteMaxSec is how long a remote turn may wait for its answer; 0 = unknown. */
export function remoteMaxSec(a: Pick<AgentItem, "kind" | "remote" | "slack_remote">): number {
  if (isSlackRemote(a)) return a.slack_remote?.max_sec_effective ?? 0;
  return a.remote?.timeout_sec ?? 0;
}

/** remoteWaitLabel replaces "typing" for a remote agent: it is not
    writing, wick is waiting for the other side. "Waiting for @halodev's
    reply · 12s / 180s"; a channel target waits for a reply in #ops. */
export function remoteWaitLabel(a: Pick<AgentItem, "kind" | "handle" | "remote" | "slack_remote">, elapsedSec: number): string {
  const s = a.slack_remote;
  const who = s && s.target !== "dm" ? `a reply in ${targetLabel(s)}` : `@${a.handle}'s reply`;
  const max = remoteMaxSec(a);
  const secs = Math.max(0, Math.floor(elapsedSec));
  return `Waiting for ${who} · ${secs}s${max > 0 ? ` / ${max}s` : ""}`;
}


/* A2A remote agents in the Team app (plan §6.2b): another system's agent
   that wick talks to as an A2A client. Nothing runs locally, so the chat
   has no rail, Settings has no Persona/Access/Tools/Captain, and every
   word here is about the remote host. No components: the tests import it. */

export const REMOTE_KIND = "a2a-remote";
export const PLUGIN_REMOTE_KIND = "plugin-remote";

/** Any remote agent, A2A or Slack: no rail, no persona, its own Settings. */
export function isRemoteAgent(a: Pick<AgentItem, "kind"> | null | undefined): boolean {
  return a?.kind === REMOTE_KIND || a?.kind === SLACK_REMOTE_KIND || a?.kind === PLUGIN_REMOTE_KIND;
}

export function isA2ARemote(a: Pick<AgentItem, "kind"> | null | undefined): boolean {
  return a?.kind === REMOTE_KIND;
}

export function isSlackRemote(a: Pick<AgentItem, "kind"> | null | undefined): boolean {
  return a?.kind === SLACK_REMOTE_KIND;
}

/** The roster badge of a remote agent ("" for a wick agent). */
export function remoteBadge(a: Pick<AgentItem, "kind">): string {
  return isSlackRemote(a) ? "Slack remote" : isA2ARemote(a) ? "A2A remote" : a.kind === PLUGIN_REMOTE_KIND ? "Plugin remote" : "";
}

/** Sources + Agent › Remote agent offers. */
export type RemoteSource = "a2a" | "slack" | "plugin";
export const REMOTE_SOURCES: { value: RemoteSource; label: string; hint: string }[] = [
  { value: "a2a", label: "A2A", hint: "An agent that speaks the A2A protocol." },
  { value: "slack", label: "Slack", hint: "A bot or person you reach in a Slack DM, channel or thread." },
  { value: "plugin", label: "Plugin", hint: "A service plugin on this host that offers a remote agent source." },
];

/** Every rail tab: a remote agent has no project tools to show. */
export const REMOTE_HIDDEN_TABS: RailTab[] = [
  "files", "process", "workspace", "scheduled", "browser", "source", "subagents", "ticket", "notes", "todos",
];

export const REMOTE_RAIL_NOTE = "No local tools — this agent runs on its own host.";

/** The composer caption: where the turn goes and that wick adds nothing. */
export function remoteCaption(host: string | null | undefined): string {
  return `via A2A · ${host || "remote"} · no local tools`;
}

/** Settings tabs of a remote agent, Remote A2A first (the default). */
export const REMOTE_SETTINGS_TABS: { id: SettingsTab; label: string }[] = [
  { id: "remote", label: "Remote A2A" },
  { id: "mention", label: "Mention" },
  { id: "avatar", label: "Avatar" },
  { id: "advanced", label: "Advanced" },
];

/** A Slack remote agent's tabs: the same set, its first one named Remote. */
export const SLACK_REMOTE_SETTINGS_TABS: { id: SettingsTab; label: string }[] = REMOTE_SETTINGS_TABS.map((t) =>
  t.id === "remote" ? { ...t, label: "Remote" } : t,
);

export function remoteSettingsTabs(a: Pick<AgentItem, "kind">): { id: SettingsTab; label: string }[] {
  return isSlackRemote(a) ? SLACK_REMOTE_SETTINGS_TABS : REMOTE_SETTINGS_TABS;
}

/** remoteSettingsTab maps a requested tab onto one a remote agent has;
    Persona, Access and the rest open the Remote tab. */
export function remoteSettingsTab(t: SettingsTab): SettingsTab {
  return REMOTE_SETTINGS_TABS.some((x) => x.id === t) ? t : "remote";
}

/** The create-time warning (plan §6.2b Pengaman). */
export function egressWarning(host: string | null | undefined): string {
  return `Messages you send, including other agents' output that mentions this agent, leave wick for ${host || "the remote host"}.`;
}


export const AUTH_OPTIONS: { value: RemoteAuthType; label: string }[] = [
  { value: "none", label: "None" },
  { value: "bearer", label: "Bearer token" },
  { value: "api_key", label: "API key" },
];

export const DEFAULT_API_KEY_HEADER = "X-API-Key";

/** authReq builds the auth body from the form. "none" sends no secret; a
    blank secret on bearer/api_key is undefined so a caller can tell the
    form is incomplete. */
export function authReq(type: RemoteAuthType, secret: string, header: string): RemoteAuthReq | undefined {
  if (type === "none") return { type: "none" };
  const s = secret.trim();
  if (!s) return undefined;
  if (type === "api_key") return { type, header: header.trim() || DEFAULT_API_KEY_HEADER, secret: s };
  return { type, secret: s };
}

/** hostOf reads the host of a typed URL ("" while it is not one yet). */
export function hostOf(url: string): string {
  try {
    const u = new URL(url.trim());
    return u.protocol === "http:" || u.protocol === "https:" ? u.host : "";
  } catch {
    return "";
  }
}

export const TIMEOUT_MIN = 1;
export const TIMEOUT_MAX = 900;
export const TIMEOUT_DEFAULT = 120;
export const MAX_BYTES_MIN = 1024;
export const MAX_BYTES_MAX = 32 * 1024 * 1024;
export const MAX_BYTES_DEFAULT = 2 * 1024 * 1024;

/** The Advanced form edits the response cap in KB; the server takes bytes. */
export function kbToBytes(kb: number): number {
  return Math.round(kb * 1024);
}
export function bytesToKb(b: number): number {
  return Math.round(b / 1024);
}

/** limitsError says why timeout/max bytes cannot be saved ("" = fine). */
export function limitsError(timeoutSec: number, maxBytes: number): string {
  if (!Number.isInteger(timeoutSec) || timeoutSec < TIMEOUT_MIN || timeoutSec > TIMEOUT_MAX) {
    return `Timeout must be ${TIMEOUT_MIN}–${TIMEOUT_MAX} seconds.`;
  }
  if (!Number.isFinite(maxBytes) || maxBytes < MAX_BYTES_MIN || maxBytes > MAX_BYTES_MAX) {
    return `Max response must be 1 KB–${MAX_BYTES_MAX / 1024 / 1024} MB.`;
  }
  return "";
}

/** formatBytes for the card preview and Advanced hints. */
export function formatBytes(n: number): string {
  if (n >= 1024 * 1024) return `${+(n / 1024 / 1024).toFixed(1)} MB`;
  if (n >= 1024) return `${Math.round(n / 1024)} KB`;
  return `${n} B`;
}

/** testSummary words a Test result for the line under the button. */
export function testSummary(r: { ok: boolean; state: string; latency_ms: number; reply: string; error: string }): string {
  if (r.ok) {
    const reply = r.reply.trim().replace(/\s+/g, " ");
    return `Replied in ${r.latency_ms} ms${reply ? ` — “${reply.length > 120 ? `${reply.slice(0, 117)}…` : reply}”` : ""}`;
  }
  const step = r.state === "card_failed" ? "Agent card" : r.state === "client_failed" ? "Client" : r.state === "send_failed" ? "Ping" : r.state || "Test";
  return `${step} failed${r.error ? `: ${r.error}` : ""}`;
}

/** remoteChatMode is what DetailView's agentMode differs in for a remote
    agent: no rail tabs, a footer saying why, the A2A composer caption. */
export function remoteChatMode(a: Pick<AgentItem, "remote" | "kind" | "slack_remote">): { hideTabs: RailTab[]; railNote: string; caption: string } {
  if (isSlackRemote(a)) {
    return {
      hideTabs: [...REMOTE_HIDDEN_TABS],
      railNote: "No local tools — this agent answers in Slack.",
      caption: slackCaption(a.slack_remote ? targetLabel(a.slack_remote) : ""),
    };
  }
  return { hideTabs: [...REMOTE_HIDDEN_TABS], railNote: REMOTE_RAIL_NOTE, caption: remoteCaption(a.remote?.host) };
}

/** remoteSubtitle is the chat header's second line: badge, card version, host.
    A Slack target named like the agent's own handle is left out, so the
    line does not read "@halodev · Slack remote · @halodev". */
export function remoteSubtitle(a: Pick<AgentItem, "remote" | "kind" | "slack_remote"> & { handle?: string }): string {
  if (isSlackRemote(a)) {
    const target = a.slack_remote ? targetLabel(a.slack_remote) : "";
    const same = !!a.handle && target.replace(/^@/, "").toLowerCase() === a.handle.toLowerCase();
    return ["Slack remote", same ? "" : target].filter(Boolean).join(" · ");
  }
  const r = a.remote;
  if (!r) return "A2A remote";
  return ["A2A remote", r.card?.version ? `v${r.card.version}` : "", r.host].filter(Boolean).join(" · ");
}
