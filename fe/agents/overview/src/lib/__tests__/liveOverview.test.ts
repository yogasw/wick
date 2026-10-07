import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { followOverview, type OverviewPort } from "../liveOverview.js";

function fakePort() {
  const posted: unknown[] = [];
  const p: OverviewPort & { posted: unknown[]; send(msg: unknown): void } = {
    posted,
    onmessage: null,
    postMessage: (m) => { posted.push(m); },
    send(msg) { this.onmessage?.({ data: msg } as MessageEvent); },
  };
  return p;
}

describe("followOverview", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  test("joins the shared stream; no timer reload while it is up; a pool burst reloads once", async () => {
    const load = vi.fn();
    const port = fakePort();
    const stop = followOverview("/b", load, port);
    expect(port.posted).toContainEqual({ type: "subscribe-lifecycle", base: "/b" });
    expect(load).toHaveBeenCalledTimes(1);
    port.send({ type: "lifecycle-status", status: "connected" });
    // The old page re-read /api/overview every 3 s.
    await vi.advanceTimersByTimeAsync(5 * 60_000);
    expect(load).toHaveBeenCalledTimes(1);
    port.send({ type: "pool" });
    port.send({ type: "pool" });
    port.send({ type: "activity", event: { session_id: "x" } });
    await vi.advanceTimersByTimeAsync(1000);
    expect(load).toHaveBeenCalledTimes(2);
    stop();
    expect(port.posted).toContainEqual({ type: "unsubscribe-lifecycle" });
  });

  test("stream down: 60 s fallback poll; reconnect reloads once", async () => {
    const load = vi.fn();
    const port = fakePort();
    const stop = followOverview("/b", load, port);
    port.send({ type: "lifecycle-status", status: "connected" });
    port.send({ type: "lifecycle-status", status: "error" });
    await vi.advanceTimersByTimeAsync(59_000);
    expect(load).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(2_000);
    expect(load).toHaveBeenCalledTimes(2);
    port.send({ type: "lifecycle-status", status: "connected" });
    await vi.advanceTimersByTimeAsync(1000);
    expect(load).toHaveBeenCalledTimes(3);
    stop();
  });
});
