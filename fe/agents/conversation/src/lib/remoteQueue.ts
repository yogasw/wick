/* Where a message sent to a busy remote agent stands, and the remote's own
   page for a turn. The server records both as system turns
   (kind remote_queue / remote_link); this file turns them into what the
   thread shows. */
import { jumpLink } from "./slackDelivery.js";

export type QueueState = "queued" | "sent" | "cancelled" | "forwarded";

export interface QueueView {
  state: QueueState;
  label: string;
  canCancel: boolean;
}

type Turn = { role: string; kind?: string; text: string; extras?: Record<string, string> };

/** queueView labels a remote_queue turn. Null for a message already sent:
    the mark is let go once the message left the queue. */
export function queueView(turn: Turn, agentName = ""): QueueView | null {
  const who = agentName || "agent";
  switch (turn.extras?.state) {
    case "queued":
      return { state: "queued", label: `Queued — sends after ${who} finishes`, canCancel: true };
    case "forwarded":
      return { state: "forwarded", label: "Forwarded", canCancel: false };
    case "cancelled":
      return { state: "cancelled", label: "Cancelled", canCancel: false };
    default:
      return null;
  }
}

/** withRemoteLinks drops remote_link turns and sent queue marks from the
    thread, and hands the latest remote link to each stop row after it
    (extras.remote_link) — a remote wick cannot stop is stopped there. */
export function withRemoteLinks<T extends Turn>(turns: T[]): T[] {
  let link = "";
  const out: T[] = [];
  for (const t of turns) {
    if (t.role === "system" && t.kind === "remote_link") {
      link = jumpLink(t.extras?.url);
      continue;
    }
    if (t.role === "system" && t.kind === "remote_queue" && !queueView(t)) continue;
    if (t.role === "system" && t.kind === "interrupted" && link) {
      out.push({ ...t, extras: { ...t.extras, remote_link: link } });
      continue;
    }
    out.push(t);
  }
  return out;
}
