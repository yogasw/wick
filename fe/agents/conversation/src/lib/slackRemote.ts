import type { SlackRemoteConfig, SlackTarget, SlackListen, SlackTestResult } from "./api/team.js";

/* Slack remote agents in the Team app: wick posts each turn to a DM,
   channel or thread in Slack and reads the reply back. Words and checks
   shared by the + Agent wizard and Settings. No components: the tests
   import it. */

export const SLACK_REMOTE_KIND = "slack-remote";

export const TARGET_OPTIONS: { value: SlackTarget; label: string; hint: string }[] = [
  { value: "dm", label: "DM", hint: "A direct message to a user or bot." },
  { value: "channel", label: "Channel", hint: "Each chat opens a new thread in the channel." },
  { value: "thread", label: "Thread", hint: "Every chat replies in one existing thread." },
];

export const LISTEN_OPTIONS: { value: SlackListen; label: string; hint: string }[] = [
  { value: "target", label: "Only the target", hint: "Replies from the DM user or the @-mentioned user count." },
  { value: "anyone", label: "Anyone in the thread", hint: "Any reply that is not from wick counts." },
];

/** Server defaults for 0 and its ceiling (slackremote.Config). */
export const IDLE_DEFAULT = 30;
/** GRACE_DEFAULT is how long late replies are still passed on (grace_sec 0). */
export const GRACE_DEFAULT = 120;
export const MAX_DEFAULT = 180;
export const SEC_CAP = 900;

/** targetLabel is how the UI names where turns go: the saved label, or
    the raw id with its sigil. A thread names its channel. */
export function targetLabel(c: Pick<SlackRemoteConfig, "target" | "channel" | "user" | "target_name">): string {
  const name = (c.target_name ?? "").trim();
  if (c.target === "dm") return name || (c.user ? `@${c.user}` : "a DM");
  const ch = name || (c.channel ? `#${c.channel}` : "a channel");
  return c.target === "thread" ? `a thread in ${ch}` : ch;
}

/** The composer caption: where the turn goes and that wick adds nothing. */
export function slackCaption(where: string | null | undefined): string {
  return `via Slack · ${where || "Slack"} · no local tools`;
}

/** The create-time warning: data leaves wick into Slack. */
export function slackWarning(where: string, workspace: string | null | undefined): string {
  return `Messages you send to this agent, including other agents' output that mentions it, will be posted to ${where} in ${workspace || "Slack"}.`;
}

/** parseSlackLink reads a message permalink
    (https://x.slack.com/archives/C123/p1700000000123456[?thread_ts=…]):
    the channel, and the thread it belongs to — the thread_ts query when
    it is a reply, else the message itself. null = not a permalink. */
export function parseSlackLink(link: string): { channel: string; thread_ts: string } | null {
  let u: URL;
  try {
    u = new URL(link.trim());
  } catch {
    return null;
  }
  const m = u.pathname.match(/\/archives\/([A-Z0-9]+)\/p(\d{10})(\d{6})\/?$/);
  if (!m) return null;
  const q = u.searchParams.get("thread_ts") ?? "";
  return { channel: m[1], thread_ts: /^\d+\.\d+$/.test(q) ? q : `${m[2]}.${m[3]}` };
}

/** secError says why an idle/max pair cannot be saved ("" = fine). */
export function secError(idle: number, max: number): string {
  for (const [n, v] of [["Idle", idle], ["Max", max]] as const) {
    if (!Number.isInteger(v) || v < 0 || v > SEC_CAP) return `${n} must be 0–${SEC_CAP} seconds (0 = default).`;
  }
  return "";
}

/** configError mirrors the server's Normalize so Next/Save can say why
    before a round trip ("" = complete). */
export function configError(c: SlackRemoteConfig): string {
  if (!c.connector_id) return "Pick a Slack workspace.";
  if (c.identity === "user" && !c.account_id) return "Pick your Slack account to post as you.";
  if (c.target === "dm" && !c.user?.trim()) return "Enter the user or bot ID to DM.";
  if (c.target === "channel" && !c.channel?.trim()) return "Enter the channel ID.";
  if (c.target === "thread" && (!c.channel?.trim() || !c.thread_ts?.trim())) return "Paste the thread link, or enter its channel and timestamp.";
  return secError(c.idle_sec ?? 0, c.max_sec ?? 0);
}

/** cleanConfig drops the fields the target does not use, so a switched
    target does not carry a stale channel or user to the server. */
export function cleanConfig(c: SlackRemoteConfig): SlackRemoteConfig {
  const out: SlackRemoteConfig = {
    connector_id: c.connector_id,
    identity: c.identity,
    target: c.target,
    listen: c.listen,
    marker: c.marker ?? true,
    mention_target: c.mention_target ?? true,
    idle_sec: c.idle_sec ?? 0,
    max_sec: c.max_sec ?? 0,
  };
  // Sent only when set: the saved config is replaced whole, so 0 = default.
  if ((c.poll_sec ?? 0) > 0) out.poll_sec = c.poll_sec;
  if ((c.grace_sec ?? 0) !== 0) out.grace_sec = c.grace_sec;
  if (c.identity === "user" && c.account_id) out.account_id = c.account_id;
  const t = (v?: string) => (v ?? "").trim();
  if (c.target === "dm") out.user = t(c.user);
  else out.channel = t(c.channel);
  if (c.target === "channel" && t(c.mention_id)) out.mention_id = t(c.mention_id);
  if (c.target === "thread") out.thread_ts = t(c.thread_ts);
  if (t(c.target_name)) out.target_name = t(c.target_name);
  if (c.usage) out.usage = c.usage;
  return out;
}

/** patchBody is cleanConfig for PATCH, which only overwrites the fields
    it is sent: a cleared or unused field goes out as "" so the stored
    value does not linger. */
export function patchBody(c: SlackRemoteConfig): SlackRemoteConfig {
  const blank = { account_id: "", user: "", channel: "", mention_id: "", thread_ts: "", target_name: "" };
  return { ...blank, ...cleanConfig(c) };
}

/** slackTestSummary words a Test result for the line under the button. */
export function slackTestSummary(r: SlackTestResult): string {
  if (r.ok) {
    const reply = (r.reply ?? "").trim().replace(/\s+/g, " ");
    return `Replied in ${r.latency_ms} ms${reply ? ` — “${reply.length > 120 ? `${reply.slice(0, 117)}…` : reply}”` : ""}`;
  }
  const err = r.error ? `: ${r.error}` : "";
  if (r.state === "no_reply") return `Ping posted, no reply yet${err}`;
  if (r.state === "send_failed") return `Ping failed${err}`;
  if (r.state === "auth_failed") return `Slack sign-in failed${err}`;
  return `Test failed${err}`;
}
