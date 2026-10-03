import type { AgentItem, RemoteAuthReq, RemoteAuthType, RemoteUsage } from "./api/team.js";
import type { RailTab } from "./agentMode.js";
import type { SettingsTab } from "./agentsRouter.js";

/* A2A remote agents in the Team app (plan §6.2b): another system's agent
   that wick talks to as an A2A client. Nothing runs locally, so the chat
   has no rail, Settings has no Persona/Access/Tools/Captain, and every
   word here is about the remote host. No components: the tests import it. */

export const REMOTE_KIND = "a2a-remote";

export function isRemoteAgent(a: Pick<AgentItem, "kind"> | null | undefined): boolean {
  return a?.kind === REMOTE_KIND;
}

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

/** remoteSettingsTab maps a requested tab onto one a remote agent has;
    Persona, Access and the rest open Remote A2A. */
export function remoteSettingsTab(t: SettingsTab): SettingsTab {
  return REMOTE_SETTINGS_TABS.some((x) => x.id === t) ? t : "remote";
}

/** The create-time warning (plan §6.2b Pengaman). */
export function egressWarning(host: string | null | undefined): string {
  return `Messages you send, including other agents' output that mentions this agent, leave wick for ${host || "the remote host"}.`;
}

export const USAGE_OPTIONS: { value: RemoteUsage; label: string; hint: string }[] = [
  { value: "only_me", label: "Only me", hint: "Only you can chat with it. Your agents' @mentions are refused." },
  {
    value: "me_and_my_agents",
    label: "Me + my agents (via mention)",
    hint: "Your agents may @mention it too; only the mention text is sent, and the turn cap still applies.",
  },
];

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
