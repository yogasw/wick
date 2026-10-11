import { connectSessionsStream, type SessionActivity, type SessionsStreamHandlers, type SessionsPort } from "./stores/sessionsStream.js";

/* How the Team roster stays live without a steady poll. Everything rides
   the per-user /stream/sessions stream: `activity` for each turn step,
   `agent_changed` when an agent or group the user sees is created, edited,
   shared, unshared or deleted. A signal triggers one REST read of the
   roster (debounced: one save can signal more than once), a reconnect
   reads everything afresh rather than replaying what was missed, and only
   while the stream is down does a slow poll stand in for it. */

/** agent_changed coalesces into one roster read per burst. */
export const ROSTER_REFETCH_DEBOUNCE_MS = 300;
/** The stand-in poll, only while /stream/sessions is down. */
export const ROSTER_FALLBACK_POLL_MS = 60000;
/** A steady stream of turn starts and ends still reads the roster at
    least this often: the debounce cannot be put off forever. */
export const ROSTER_REFETCH_MAX_WAIT_MS = 2000;

export type LiveRosterHandlers = {
  /** Read the roster (agents and groups) again. */
  reload: () => void;
  /** The stream came back after dropping: what it missed meanwhile
      (a turn's end) is gone, so per-turn bookkeeping starts over. */
  onReconnect?: () => void;
  onActivity?: (ev: SessionActivity) => void;
};

type Connect = (base: string, h: SessionsStreamHandlers, port?: SessionsPort) => () => void;

/** liveRoster joins the stream for the roster; the returned function
    leaves it and clears every timer. connect overrides the stream (tests). */
export function liveRoster(base: string, h: LiveRosterHandlers, connect: Connect = connectSessionsStream): () => void {
  let dropped = false;
  let refetch: ReturnType<typeof setTimeout> | null = null;
  let fallback: ReturnType<typeof setInterval> | null = null;
  const stopFallback = () => {
    if (fallback) clearInterval(fallback);
    fallback = null;
  };
  const leave = connect(base, {
    onActivity: (ev) => h.onActivity?.(ev),
    onAgentChanged: () => {
      if (refetch) clearTimeout(refetch);
      refetch = setTimeout(() => {
        refetch = null;
        h.reload();
      }, ROSTER_REFETCH_DEBOUNCE_MS);
    },
    onStatus: (s) => {
      if (s === "error") {
        dropped = true;
        fallback ??= setInterval(() => {
          if (typeof document === "undefined" || document.visibilityState === "visible") h.reload();
        }, ROSTER_FALLBACK_POLL_MS);
        return;
      }
      stopFallback();
      if (dropped) {
        dropped = false;
        h.onReconnect?.();
        h.reload();
      }
    },
  });
  return () => {
    if (refetch) clearTimeout(refetch);
    stopFallback();
    leave();
  };
}

/** boundedDebounce runs fn once a burst of calls goes quiet for wait ms,
    but no later than maxWait ms after the burst's first call, so a steady
    stream of calls still runs it every maxWait. cancel drops a pending run. */
export function boundedDebounce(fn: () => void, wait: number, maxWait: number): { call: () => void; cancel: () => void } {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let first = 0;
  const fire = () => {
    timer = null;
    first = 0;
    fn();
  };
  return {
    call() {
      const now = Date.now();
      if (!first) first = now;
      if (timer) clearTimeout(timer);
      timer = setTimeout(fire, Math.max(0, Math.min(wait, first + maxWait - now)));
    },
    cancel() {
      if (timer) clearTimeout(timer);
      timer = null;
      first = 0;
    },
  };
}

/** singleFlight shares one run of fn among callers while it is in flight,
    and a call that arrives meanwhile runs fn once more after it: the run
    in flight may have read before what the caller wants to see. */
export function singleFlight(fn: () => Promise<void>): () => Promise<void> {
  let inflight: Promise<void> | null = null;
  let again: Promise<void> | null = null;
  const start = (): Promise<void> => {
    inflight = fn().finally(() => {
      inflight = null;
    });
    return inflight;
  };
  return () => {
    if (!inflight) return start();
    again ??= inflight
      .catch(() => {})
      .then(() => {
        again = null;
        return inflight ?? start();
      });
    return again;
  };
}
