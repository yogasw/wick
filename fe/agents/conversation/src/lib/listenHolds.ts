/* Slack listen / auto-reply holds (listen_held system turns, folded on
   hold_id) as the thread shows them: a hold that ended well is no row of
   its own, its detail sits on the timestamp of the user turn it became; a
   hold that ended badly stays a row, drawn as a problem. */

export type ListenHold = {
  mode: "auto_reply" | "listen";
  state: string;
  delaySec: number;
  /** When wick received the reply (epoch ms), 0 when unknown. */
  startedMs: number;
  /** "3s": how long it was held, from the server's "after 3s". */
  heldFor: string;
  msgTs: string;
  /** When the agent reads the thread (epoch ms), 0 when unknown. */
  deadlineMs?: number;
  /** How many replies the hold heard (thread_heard rows). */
  heard?: number;
};

type HoldTurn = {
  turn_id?: string;
  role: string;
  kind?: string;
  text: string;
  extras?: Record<string, string>;
  sender?: { permalink?: string };
};

/** A hold that never reached the agent. */
export function isProblemHold(state: string | undefined): boolean {
  return state === "skipped" || state === "cancelled";
}

/** The Slack ts of a message permalink (".../p1729651234123456" →
    "1729651234.123456"), "" when it has none. */
export function permalinkTs(link: string | undefined): string {
  const m = /\/p(\d{10})(\d{6})(?:[?#]|$)/.exec(link ?? "");
  return m ? `${m[1]}.${m[2]}` : "";
}

export function holdOf(t: HoldTurn): ListenHold {
  const e = t.extras ?? {};
  return {
    mode: e.mode === "auto_reply" ? "auto_reply" : "listen",
    state: e.state ?? "",
    delaySec: Number(e.delay_sec) || 0,
    startedMs: Number(e.started_ms) || 0,
    heldFor: /after (\S+)\s*$/.exec(t.text ?? "")?.[1] ?? "",
    msgTs: e.msg_ts ?? "",
    deadlineMs: Number(e.deadline_ms) || 0,
    heard: Number(e.heard) || 0,
  };
}

/** Where a heard reply stands, from its hold's latest state: "unread"
    while the hold waits, "read" once the agent read it, "dropped" when the
    hold never reached the agent. */
export type HeardStatus = "unread" | "read" | "dropped";

export function heardStatusOf(state: string | undefined): HeardStatus {
  if (state === "held" || !state) return "unread";
  return isProblemHold(state) ? "dropped" : "read";
}

/** linkListenHolds drops the listen_held rows that did not end badly and
    returns each one's detail by the turn_id of the user turn it became.
    The link is exact when that turn's Slack permalink carries the hold's
    msg_ts. Otherwise it is the first user turn AFTER the hold row: the row
    is written (and stays, once folded) when the reply arrives, before the
    turn it becomes exists, so the message is never the turn before it. The
    search stops at the next listen_held row, so a hold never claims a turn
    of a later one; a hold still waiting has no turn yet and links nothing. */
export function linkListenHolds<T extends HoldTurn>(turns: T[]): { turns: T[]; holds: Record<string, ListenHold>; heard: Record<string, HeardStatus> } {
  const holds: Record<string, ListenHold> = {};
  const heard: Record<string, HeardStatus> = {};
  const out: T[] = [];
  // A hold still waiting is the thread's countdown: it sits last, under
  // the replies it heard, not where its first reply was.
  const waiting: T[] = [];
  const byTs = new Map<string, string>();
  for (const t of turns) {
    const ts = t.role === "user" ? permalinkTs(t.sender?.permalink) : "";
    if (ts && t.turn_id) byTs.set(ts, t.turn_id);
  }
  turns.forEach((t, i) => {
    if (t.role === "system" && t.kind === "listen_held" && t.extras?.hold_id) {
      heard[t.extras.hold_id] = heardStatusOf(t.extras.state);
    }
    if (t.role !== "system" || t.kind !== "listen_held" || isProblemHold(t.extras?.state)) {
      out.push(t);
      return;
    }
    const h = holdOf(t);
    if (h.state === "held") {
      waiting.push(t);
      return;
    }
    let id = h.msgTs ? byTs.get(h.msgTs) : undefined;
    if (!id && h.state === "sent") {
      for (let j = i + 1; j < turns.length; j++) {
        const n = turns[j];
        if (n.role === "system" && n.kind === "listen_held") break;
        if (n.role === "user") { id = n.turn_id; break; }
      }
    }
    if (id && !holds[id]) holds[id] = h;
  });
  return { turns: [...out, ...waiting], holds, heard };
}
