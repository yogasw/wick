import { describe, test, expect, beforeEach, afterEach, vi } from "vitest";

/* The worker is a module with top-level side effects (it assigns
   self.onconnect), so each test imports it fresh against stubbed globals.

   What these tests pin down is arithmetic, not style: a browser allows ~6
   concurrent HTTP/1.1 connections per origin and an SSE stream holds its
   slot for the life of the page. One stream per session (and per tab)
   exhausted that quota at about four open conversations, leaving every later
   request stuck at "pending" — indistinguishable from a hung server. The
   worker therefore multiplexes ALL sessions onto one /stream/multi
   connection and shares one /stream/sessions lifecycle connection. */

interface FakeES {
  url: string;
  readyState: number;
  listeners: Record<string, ((ev: MessageEvent) => void)[]>;
  onopen: (() => void) | null;
  onerror: (() => void) | null;
  closed: boolean;
  emit(event: string, data: unknown): void;
  open(): void;
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
    open(): void {
      this.onopen?.();
    }
    close(): void {
      this.closed = true;
      this.readyState = CLOSED;
    }
  }

  vi.stubGlobal("EventSource", StubEventSource);
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve({ json: () => Promise.resolve([]) })));
}

/* A MessagePort stand-in that records what the worker posted back and can
   fire its own "close" event (the dead-page signal). */
function makePort() {
  const received: unknown[] = [];
  const closeCbs: (() => void)[] = [];
  const port = {
    received,
    onmessage: null as ((e: MessageEvent) => void) | null,
    postMessage(msg: unknown) { received.push(msg); },
    addEventListener(t: string, cb: () => void) { if (t === "close") closeCbs.push(cb); },
    fireClose() { closeCbs.forEach((cb) => cb()); },
    start() {},
    close() {},
  };
  return port;
}

type Port = ReturnType<typeof makePort>;
type Connectable = { onconnect: ((e: MessageEvent) => void) | null };

async function loadWorker() {
  vi.resetModules();
  installStubs();
  await import("./sse-worker.js");
  const scope = self as unknown as Connectable;
  return function connect(): Port {
    const port = makePort();
    scope.onconnect?.({ ports: [port] } as unknown as MessageEvent);
    return port;
  };
}

function subscribe(port: Port, sid: string): void {
  port.onmessage?.({ data: { type: "subscribe", sessionID: sid, base: "http://x" } } as MessageEvent);
}

/* The mux reopen is debounced 150ms so a page mounting several panels does
   not churn connections; tests advance fake timers past it. */
function settle(): void {
  vi.advanceTimersByTime(200);
}

const live = () => created.filter((c) => !c.closed);

describe("multiplexed session stream", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  test("N sessions ride ONE connection", async () => {
    const connect = await loadWorker();
    const port = connect();
    subscribe(port, "b");
    subscribe(port, "a");
    subscribe(port, "c");
    settle();

    expect(live()).toHaveLength(1);
    // Sorted set → stable URL → no spurious reopens for the same set.
    expect(live()[0].url).toBe("http://x/stream/multi?sessions=a%2Cb%2Cc");
  });

  test("events are routed to their session's subscribers by session_id", async () => {
    const connect = await loadWorker();
    const pa = connect();
    const pb = connect();
    subscribe(pa, "sess-a");
    subscribe(pb, "sess-b");
    settle();

    live()[0].emit("agent", { session_id: "sess-a", type: "text_delta", data: "hi" });

    const evOf = (p: Port) => p.received.filter((m) => (m as { type?: string }).type === "event");
    expect(evOf(pa)).toHaveLength(1);
    expect(evOf(pb)).toHaveLength(0);
    expect((evOf(pa)[0] as { sessionID: string }).sessionID).toBe("sess-a");
  });

  test("subscribing another session rebuilds the stream with the larger set", async () => {
    const connect = await loadWorker();
    const port = connect();
    subscribe(port, "one");
    settle();
    expect(live()[0].url).toContain("sessions=one");

    subscribe(port, "two");
    settle();

    expect(live()).toHaveLength(1); // old one closed, not leaked
    expect(live()[0].url).toBe("http://x/stream/multi?sessions=one%2Ctwo");
  });

  test("unsubscribing the last subscriber of a session drops it from the set", async () => {
    const connect = await loadWorker();
    const port = connect();
    subscribe(port, "keep");
    subscribe(port, "drop");
    settle();

    port.onmessage?.({ data: { type: "unsubscribe", sessionID: "drop" } } as MessageEvent);
    settle();

    expect(live()).toHaveLength(1);
    expect(live()[0].url).toBe("http://x/stream/multi?sessions=keep");
  });

  /* The reload-zombie regression: a page that dies without posting its
     unsubscribes (reload, tab close) must not pin its sessions' slot in the
     stream forever. Where the browser fires MessagePort "close", the worker
     reaps the dead port and rebuilds the set. */
  test("a dead port's sessions are reaped via the port close event", async () => {
    const connect = await loadWorker();
    const alivePort = connect();
    const deadPort = connect();
    subscribe(alivePort, "stays");
    subscribe(deadPort, "zombie");
    settle();
    expect(live()[0].url).toContain("stays");
    expect(live()[0].url).toContain("zombie");

    deadPort.fireClose(); // the page reloaded; no unsubscribe was ever sent
    settle();

    expect(live()).toHaveLength(1);
    expect(live()[0].url).toBe("http://x/stream/multi?sessions=stays");
  });

  test("closing every subscriber closes the connection entirely", async () => {
    const connect = await loadWorker();
    const port = connect();
    subscribe(port, "only");
    settle();
    expect(live()).toHaveLength(1);

    port.onmessage?.({ data: { type: "unsubscribe", sessionID: "only" } } as MessageEvent);
    settle();

    expect(live()).toHaveLength(0);
  });
});

describe("lifecycle stream sharing", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  const subLifecycle = (p: Port) =>
    p.onmessage?.({ data: { type: "subscribe-lifecycle", base: "http://x" } } as MessageEvent);

  test("three subscribers still share ONE connection", async () => {
    const connect = await loadWorker();
    const ports = [connect(), connect(), connect()];
    ports.forEach(subLifecycle);

    expect(created).toHaveLength(1);
    expect(created[0].url).toBe("http://x/stream/sessions");
  });

  test("every subscriber receives each session event", async () => {
    const connect = await loadWorker();
    const ports = [connect(), connect(), connect()];
    ports.forEach(subLifecycle);

    created[0].emit("session", { session_id: "s1", lifecycle: "running" });

    ports.forEach((p) => {
      const events = p.received.filter((m) => (m as { type?: string }).type === "session");
      expect(events).toHaveLength(1);
      expect(events[0]).toEqual({
        type: "session",
        event: { session_id: "s1", lifecycle: "running" },
      });
    });
  });

  test("connection closes only when the LAST subscriber leaves", async () => {
    const connect = await loadWorker();
    const a = connect();
    const b = connect();
    [a, b].forEach(subLifecycle);

    a.onmessage?.({ data: { type: "unsubscribe-lifecycle" } } as MessageEvent);
    expect(created[0].closed).toBe(false);

    b.onmessage?.({ data: { type: "unsubscribe-lifecycle" } } as MessageEvent);
    expect(created[0].closed).toBe(true);
  });

  test("a dead port is reaped from the lifecycle set too", async () => {
    const connect = await loadWorker();
    const only = connect();
    subLifecycle(only);
    expect(created[0].closed).toBe(false);

    only.fireClose();

    expect(created[0].closed).toBe(true);
  });

  test("subscribe without a base is ignored (no connection opened)", async () => {
    const connect = await loadWorker();
    const port = connect();
    port.onmessage?.({ data: { type: "subscribe-lifecycle" } } as MessageEvent);
    expect(created).toHaveLength(0);
  });
});
