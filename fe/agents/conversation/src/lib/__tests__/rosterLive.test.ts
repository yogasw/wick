import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { liveRoster, ROSTER_REFETCH_DEBOUNCE_MS, ROSTER_FALLBACK_POLL_MS } from "../rosterLive.js";
import type { SessionsStreamHandlers } from "../stores/sessionsStream.js";

/* A fake stream: the test drives the handlers liveRoster registered. */
function fakeStream() {
  const s = { h: null as SessionsStreamHandlers | null, closed: false };
  const connect = (_base: string, h: SessionsStreamHandlers) => {
    s.h = h;
    return () => { s.closed = true; };
  };
  return { s, connect };
}

describe("liveRoster", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  test("a burst of agent_changed reads the roster once", () => {
    const { s, connect } = fakeStream();
    const reload = vi.fn();
    liveRoster("/b", { reload }, connect);
    s.h!.onAgentChanged!({ agent_id: "a1" });
    s.h!.onAgentChanged!({ agent_id: "a1" });
    s.h!.onAgentChanged!({ group_id: "g1" });
    expect(reload).not.toHaveBeenCalled();
    vi.advanceTimersByTime(ROSTER_REFETCH_DEBOUNCE_MS);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  test("no poll while connected; a slow one while down; a read on reconnect", () => {
    const { s, connect } = fakeStream();
    const reload = vi.fn();
    liveRoster("/b", { reload }, connect);
    s.h!.onStatus!("connected");
    vi.advanceTimersByTime(ROSTER_FALLBACK_POLL_MS * 3);
    expect(reload).not.toHaveBeenCalled();

    s.h!.onStatus!("error");
    vi.advanceTimersByTime(ROSTER_FALLBACK_POLL_MS);
    expect(reload).toHaveBeenCalledTimes(1);

    s.h!.onStatus!("connected");
    expect(reload).toHaveBeenCalledTimes(2);
    vi.advanceTimersByTime(ROSTER_FALLBACK_POLL_MS * 3);
    expect(reload).toHaveBeenCalledTimes(2);
  });

  test("activity passes through; leaving clears timers and closes", () => {
    const { s, connect } = fakeStream();
    const reload = vi.fn();
    const onActivity = vi.fn();
    const leave = liveRoster("/b", { reload, onActivity }, connect);
    s.h!.onActivity!({ session_id: "s1" } as never);
    expect(onActivity).toHaveBeenCalledTimes(1);
    s.h!.onAgentChanged!({ agent_id: "a1" });
    s.h!.onStatus!("error");
    leave();
    vi.advanceTimersByTime(ROSTER_FALLBACK_POLL_MS * 2);
    expect(reload).not.toHaveBeenCalled();
    expect(s.closed).toBe(true);
  });
});
