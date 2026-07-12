import { describe, test, expect, beforeEach, vi } from "vitest";
import { get } from "svelte/store";
import { connectSession } from "../sse.js";
import type { AgentEvent } from "../../types/agents.js";
import type { SSETransport } from "../sse.js";

function makeFakeTransport(): SSETransport & {
  posted: unknown[];
  push(msg: unknown): void;
} {
  let handler: ((msg: unknown) => void) | null = null;
  const posted: unknown[] = [];
  return {
    posted,
    post(msg: unknown) {
      posted.push(msg);
    },
    onMessage(cb: (msg: unknown) => void) {
      handler = cb;
    },
    push(msg: unknown) {
      handler?.(msg);
    },
    close() {},
  };
}

describe("connectSession", () => {
  let transport: ReturnType<typeof makeFakeTransport>;

  beforeEach(() => {
    transport = makeFakeTransport();
  });

  test("posts subscribe with sessionID and base on connect", () => {
    connectSession("http://localhost", "sess-1", { transport });
    expect(transport.posted).toHaveLength(1);
    expect(transport.posted[0]).toEqual({
      type: "subscribe",
      sessionID: "sess-1",
      base: "http://localhost",
    });
  });

  test("invokes event callback when worker pushes {type:event}", () => {
    const received: AgentEvent[] = [];
    const stream = connectSession("http://localhost", "sess-2", { transport });
    stream.onEvent((e) => received.push(e));

    const ev: AgentEvent = { type: "text_delta", data: "hello" };
    transport.push({ type: "event", sessionID: "sess-2", event: ev });

    expect(received).toHaveLength(1);
    expect(received[0]).toEqual(ev);
  });

  test("status store starts as connecting then updates to connected", () => {
    const stream = connectSession("http://localhost", "sess-3", { transport });
    expect(get(stream.status)).toBe("connecting");

    transport.push({ type: "status", sessionID: "sess-3", status: "connected" });
    expect(get(stream.status)).toBe("connected");
  });

  test("status store updates to error on error status", () => {
    const stream = connectSession("http://localhost", "sess-4", { transport });
    transport.push({ type: "status", sessionID: "sess-4", status: "error" });
    expect(get(stream.status)).toBe("error");
  });

  test("close() posts unsubscribe", () => {
    const stream = connectSession("http://localhost", "sess-5", { transport });
    stream.close();
    expect(transport.posted).toContainEqual({
      type: "unsubscribe",
      sessionID: "sess-5",
    });
  });

  test("close() stops delivering events afterward", () => {
    const received: AgentEvent[] = [];
    const stream = connectSession("http://localhost", "sess-6", { transport });
    stream.onEvent((e) => received.push(e));
    stream.close();

    transport.push({ type: "event", sessionID: "sess-6", event: { type: "text_delta" } });
    expect(received).toHaveLength(0);
  });

  test("resync() re-posts subscribe so the worker refetches/reconnects", () => {
    const stream = connectSession("http://localhost", "sess-7", { transport });
    transport.posted.length = 0; // drop the initial subscribe
    stream.resync();
    expect(transport.posted).toContainEqual({
      type: "subscribe",
      sessionID: "sess-7",
      base: "http://localhost",
    });
  });

  test("resync() invokes transport.resync when the transport provides one", () => {
    const resync = vi.fn();
    const t: SSETransport = { ...makeFakeTransport(), resync };
    const stream = connectSession("http://localhost", "sess-8", { transport: t });
    stream.resync();
    expect(resync).toHaveBeenCalledOnce();
  });

  test("resync() is a no-op after close()", () => {
    const stream = connectSession("http://localhost", "sess-9", { transport });
    stream.close();
    transport.posted.length = 0;
    stream.resync();
    expect(transport.posted).toHaveLength(0);
  });
});
