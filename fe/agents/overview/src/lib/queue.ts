import { clock24 } from "@wick-fe/common-ui";
import type { QueueReason } from "./types.js";

/** waitText renders how long a session has waited: "45s", "3m", "1h 20m". */
export function waitText(ms: number): string {
  const sec = Math.max(0, Math.floor(ms / 1000));
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m`;
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  return m > 0 ? `${h}h ${m}m` : `${h}h`;
}

/** QueueExplain is the Queue card's plain-language headline: why sessions
    wait, since when, and what will let them start. */
export interface QueueExplain {
  title: string;
  detail: string;
  next: string;
  since: string;
}

export function explainQueue(r: QueueReason | null | undefined, active: number, poolMax: number, now = Date.now()): QueueExplain {
  const sinceMs = r?.since ? Date.parse(r.since) : NaN;
  const since = Number.isFinite(sinceMs) && sinceMs > 0
    ? `since ${clock24(sinceMs)} (${waitText(now - sinceMs)} ago)`
    : "";
  switch (r?.kind) {
    case "guard_hold":
      return {
        title: "Paused by Resource Guard",
        detail: r.detail ? r.detail.replace(/;?\s*stopping agent work.*$/, "") : "The machine is close to running out of CPU or memory.",
        next: r.safe_pct ? `New chats start when CPU and memory drop under ${r.safe_pct}%.` : "New chats start once CPU and memory are back to normal.",
        since,
      };
    case "slots_full":
      return {
        title: `Waiting for a free slot (${active}/${poolMax} busy)`,
        detail: "Every agent slot is running a chat.",
        next: "The next chat starts as soon as a running one finishes.",
        since,
      };
    default:
      return {
        title: "Waiting to start",
        detail: "Slots are free, but the machine's free-memory floor or the provider's own limit is holding new chats.",
        next: "They start once memory frees up or the provider has room.",
        since,
      };
  }
}
