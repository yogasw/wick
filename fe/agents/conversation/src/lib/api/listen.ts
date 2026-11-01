import { apiGetE, apiPostE } from "@wick-fe/common-api";

/* Slack listen mode of the thread a session is bound to
   (api_listen.go). A session that is no Slack channel thread answers 404:
   the chip stays hidden. */
export type ListenState = {
  listening: boolean;
  channel_id: string;
  thread_ts: string;
  delay_sec: number;
  scope: string;
  on_team_reply: string;
  instructions?: string;
  /** What this thread's own "listen" request added to LISTEN.md. */
  thread_prompt?: string;
  /** How a reply in the thread is treated now; manual = not listening. */
  mode?: ReplyMode;
  /** The channel's own mode, the one Auto-reply / Listen changes. */
  channel_mode?: ReplyMode;
  channel_name?: string;
  /** What a reply is held for in mode. */
  hold_sec?: number;
};

export type ReplyMode = "auto_reply" | "listen" | "manual";

const path = (base: string, sid: string) => `${base}/api/sessions/${encodeURIComponent(sid)}/listen`;
export const getListen = (base: string, sid: string) => apiGetE<ListenState>(path(base, sid));
export const setListen = (base: string, sid: string, action: "on" | "off", instructions?: string) =>
  apiPostE<ListenState>(path(base, sid), { action, instructions: instructions ?? "" });

/** The composer's mode switch: manual stops this thread, auto_reply and
    listen switch it on and set the channel's rule when it differs. */
export const setReplyMode = (base: string, sid: string, mode: ReplyMode) =>
  apiPostE<ListenState>(path(base, sid), { action: "mode", mode });

/** "60s" / "2m" / "1m30s" for a delay in seconds. */
export function delayText(sec: number): string {
  if (sec < 60) return `${sec}s`;
  const m = Math.floor(sec / 60), s = sec % 60;
  return s ? `${m}m${s}s` : `${m}m`;
}
