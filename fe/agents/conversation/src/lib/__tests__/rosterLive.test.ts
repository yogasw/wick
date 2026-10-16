import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { liveRoster, boundedDebounce, singleFlight, ROSTER_REFETCH_DEBOUNCE_MS, ROSTER_FALLBACK_POLL_MS } from "../rosterLive.js";
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

describe("liveRoster reconnect", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  test("onReconnect runs before the read on a reconnect only, not on the first connect", () => {
    const { s, connect } = fakeStream();
    const order: string[] = [];
    liveRoster("/b", { reload: () => order.push("reload"), onReconnect: () => order.push("reset") }, connect);
    s.h!.onStatus!("connected");
    expect(order).toEqual([]);
    s.h!.onStatus!("error");
    s.h!.onStatus!("connected");
    expect(order).toEqual(["reset", "reload"]);
  });
});

describe("boundedDebounce", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  test("a burst runs once after it goes quiet", () => {
    const fn = vi.fn();
    const d = boundedDebounce(fn, 300, 2000);
    d.call();
    vi.advanceTimersByTime(200);
    d.call();
    vi.advanceTimersByTime(299);
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  test("a steady stream still runs every maxWait, never starved", () => {
    const fn = vi.fn();
    const d = boundedDebounce(fn, 300, 2000);
    for (let t = 0; t < 5000; t += 100) {
      d.call();
      vi.advanceTimersByTime(100);
    }
    expect(fn).toHaveBeenCalledTimes(2);
    d.cancel();
    vi.advanceTimersByTime(5000);
    expect(fn).toHaveBeenCalledTimes(2);
  });
});

describe("singleFlight", () => {
  test("callers share the read in flight, and one arriving meanwhile reads once more after it", async () => {
    const resolvers: (() => void)[] = [];
    const fn = vi.fn(() => new Promise<void>((r) => resolvers.push(r)));
    const load = singleFlight(fn);
    const a = load();
    expect(load()).not.toBe(a);
    const c = load();
    expect(fn).toHaveBeenCalledTimes(1);
    resolvers[0]();
    await a;
    await Promise.resolve();
    await Promise.resolve();
    expect(fn).toHaveBeenCalledTimes(2);
    resolvers[1]();
    await c;
    // Nothing waiting: the next call is a fresh read.
    load();
    expect(fn).toHaveBeenCalledTimes(3);
  });

  test("a failed read still lets the follow-up run", async () => {
    let n = 0;
    const fn = vi.fn(() => (++n === 1 ? Promise.reject(new Error("x")) : Promise.resolve()));
    const load = singleFlight(fn);
    const a = load();
    const b = load();
    await a.catch(() => {});
    await b;
    expect(fn).toHaveBeenCalledTimes(2);
  });
});
