import { describe, test, expect, beforeEach, vi } from "vitest";

/* The worker is a module with top-level side effects (it assigns
   self.onconnect), so each test imports it fresh against stubbed globals. */

interface FakeES {
  url: string;
  readyState: number;
  listeners: Record<string, ((ev: MessageEvent) => void)[]>;
  onopen: (() => void) | null;
  onerror: (() => void) | null;
  closed: boolean;
  emit(event: string, data: unknown): void;
  addEventListener(t: string, cb: (ev: MessageEvent) => void): void;
  close(): void;
}

const OPEN = 1;
const CLOSED = 2;

let created: FakeES[] = [];

function installStubs(): void {
  created = [];

  class StubEventSource implements FakeES {
    static readonly CONNECTING = 0;
    static readonly OPEN = OPEN;
    static readonly CLOSED = CLOSED;
    url: string;
    readyState = OPEN;
    listeners: Record<string, ((ev: MessageEvent) => void)[]> = {};
    onopen: (() => void) | null = null;
    onerror: (() => void) | null = null;
    closed = false;

    constructor(url: string) {
      this.url = url;
      created.push(this);
    }
    addEventListener(t: string, cb: (ev: MessageEvent) => void): void {
      (this.listeners[t] ??= []).push(cb);
    }
    emit(event: string, data: unknown): void {
      (this.listeners[event] ?? []).forEach((cb) =>
        cb({ data: JSON.stringify(data) } as MessageEvent),
      );
    }
    close(): void {
      this.closed = true;
      this.readyState = CLOSED;
    }
  }

  vi.stubGlobal("EventSource", StubEventSource);
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve({ json: () => Promise.resolve([]) })));
}

/* A MessagePort stand-in that records what the worker posted back. */
function makePort() {
  const received: unknown[] = [];
  const port = {
    received,
    onmessage: null as ((e: MessageEvent) => void) | null,
    postMessage(msg: unknown) { received.push(msg); },
    start() {},
    close() {},
  };
  return port;
}

type Connectable = { onconnect: ((e: MessageEvent) => void) | null };

/* Load the worker fresh and hand back a `connect` helper that simulates a tab
   attaching to it. */
async function loadWorker() {
  vi.resetModules();
  installStubs();
  await import("./sse-worker.js");
  const scope = self as unknown as Connectable;
  return function connect() {
    const port = makePort();
    scope.onconnect?.({ ports: [port] } as unknown as MessageEvent);
    return port;
  };
}

describe("lifecycle stream sharing", () => {
  beforeEach(() => {
    vi.unstubAllGlobals();
  });

  test("first subscriber opens exactly one /stream/sessions connection", async () => {
    const connect = await loadWorker();
    const port = connect();
    port.onmessage?.({ data: { type: "subscribe-lifecycle", base: "http://x" } } as MessageEvent);

    expect(created).toHaveLength(1);
    expect(created[0].url).toBe("http://x/stream/sessions");
  });

  /* The whole reason this code exists: N tabs must not cost N connections.
     A browser allows only ~6 per origin, and an SSE stream holds its slot for
     the life of the tab — exceeding it left later requests unsent, which looked
     exactly like a hung server. */
  test("three subscribers still share ONE connection", async () => {
    const connect = await loadWorker();
    for (let i = 0; i < 3; i++) {
      const port = connect();
      port.onmessage?.({ data: { type: "subscribe-lifecycle", base: "http://x" } } as MessageEvent);
    }
    expect(created).toHaveLength(1);
  });

  test("every subscriber receives each session event", async () => {
    const connect = await loadWorker();
    const ports = [connect(), connect(), connect()];
    ports.forEach((p) =>
      p.onmessage?.({ data: { type: "subscribe-lifecycle", base: "http://x" } } as MessageEvent),
    );

    created[0].emit("session", { session_id: "s1", lifecycle: "running" });

    ports.forEach((p) => {
      const events = p.received.filter(
        (m) => (m as { type?: string }).type === "session",
      );
      expect(events).toHaveLength(1);
      expect(events[0]).toEqual({
        type: "session",
        event: { session_id: "s1", lifecycle: "running" },
      });
    });
  });

  test("a late subscriber joins the live stream instead of opening a second one", async () => {
    const connect = await loadWorker();
    const first = connect();
    first.onmessage?.({ data: { type: "subscribe-lifecycle", base: "http://x" } } as MessageEvent);

    const late = connect();
    late.onmessage?.({ data: { type: "subscribe-lifecycle", base: "http://x" } } as MessageEvent);

    expect(created).toHaveLength(1);
    // The newcomer is told where the shared stream stands.
    expect(late.received).toEqual([{ type: "lifecycle-status", status: "connected" }]);
  });

  test("connection closes only when the LAST subscriber leaves", async () => {
    const connect = await loadWorker();
    const a = connect();
    const b = connect();
    [a, b].forEach((p) =>
      p.onmessage?.({ data: { type: "subscribe-lifecycle", base: "http://x" } } as MessageEvent),
    );

    a.onmessage?.({ data: { type: "unsubscribe-lifecycle" } } as MessageEvent);
    expect(created[0].closed).toBe(false);

    b.onmessage?.({ data: { type: "unsubscribe-lifecycle" } } as MessageEvent);
    expect(created[0].closed).toBe(true);
  });

  test("a re-subscribe after the last leave opens a fresh stream", async () => {
    const connect = await loadWorker();
    const a = connect();
    a.onmessage?.({ data: { type: "subscribe-lifecycle", base: "http://x" } } as MessageEvent);
    a.onmessage?.({ data: { type: "unsubscribe-lifecycle" } } as MessageEvent);
    expect(created[0].closed).toBe(true);

    const b = connect();
    b.onmessage?.({ data: { type: "subscribe-lifecycle", base: "http://x" } } as MessageEvent);
    expect(created).toHaveLength(2);
    expect(created[1].closed).toBe(false);
  });

  test("subscribe without a base is ignored (no connection opened)", async () => {
    const connect = await loadWorker();
    const port = connect();
    port.onmessage?.({ data: { type: "subscribe-lifecycle" } } as MessageEvent);
    expect(created).toHaveLength(0);
  });

  test("the per-session stream is separate from the lifecycle stream", async () => {
    const connect = await loadWorker();
    const port = connect();
    port.onmessage?.({ data: { type: "subscribe-lifecycle", base: "http://x" } } as MessageEvent);
    port.onmessage?.({
      data: { type: "subscribe", sessionID: "s1", base: "http://x" },
    } as MessageEvent);

    expect(created).toHaveLength(2);
    expect(created.map((c) => c.url).sort()).toEqual([
      "http://x/stream/sessions",
      "http://x/stream?session=s1",
    ]);
  });
});
