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

export type LiveRosterHandlers = {
  /** Read the roster (agents and groups) again. */
  reload: () => void;
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
