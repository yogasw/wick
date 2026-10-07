import { describe, expect, it, vi } from "vitest";
import { connectSessionsStream, type SessionsPort } from "../sessionsStream.js";

/* The Team roster joins the SharedWorker's one /stream/sessions connection
   (subscribe-lifecycle) instead of opening its own, and hears each turn step
   as an `activity` message. */
function fakePort() {
  const sent: unknown[] = [];
  const port: SessionsPort & { sent: unknown[]; deliver(m: unknown): void } = {
    sent,
    onmessage: null,
    postMessage: (m) => void sent.push(m),
    start: vi.fn(),
    close: vi.fn(),
    deliver(m) { this.onmessage?.({ data: m } as MessageEvent); },
  };
  return port;
}

describe("connectSessionsStream", () => {
  it("joins the shared lifecycle stream and forwards activity + status", () => {
    const port = fakePort();
    const acts: unknown[] = [];
    const status: string[] = [];
    const leave = connectSessionsStream("/b", { onActivity: (e) => acts.push(e), onStatus: (s) => status.push(s) }, port);
    expect(port.sent).toEqual([{ type: "subscribe-lifecycle", base: "/b" }]);

    port.deliver({ type: "session", event: { session_id: "s1", lifecycle: "working" } });
    port.deliver({ type: "activity", event: { session_id: "s1", work: "tool", action: "Bash" } });
    port.deliver({ type: "lifecycle-status", status: "error" });
    port.deliver({ type: "lifecycle-status", status: "connected" });
    expect(acts).toEqual([{ session_id: "s1", work: "tool", action: "Bash" }]);
    expect(status).toEqual(["error", "connected"]);

    leave();
    expect(port.sent.at(-1)).toEqual({ type: "unsubscribe-lifecycle" });
  });
});
