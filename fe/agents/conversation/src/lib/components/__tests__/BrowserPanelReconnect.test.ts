import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { Effect } from "effect";

vi.mock("../../api/browser.js", async (orig) => ({
  ...(await orig<typeof import("../../api/browser.js")>()),
  listInstances: () => Effect.succeed([{ id: "c1", label: "Browser", disabled: false }]),
  listSessions: () =>
    Effect.succeed({ sessions: [{ session_id: "s1", pid: 1, browser: "chromium", created: "", tabs: [] }], maxTabs: 0 }),
  wsURL: () => "ws://test/cdp",
}));

import BrowserPanel from "../BrowserPanel.svelte";

// A WebSocket the test opens and drops by hand.
class FakeWS {
  static OPEN = 1;
  static all: FakeWS[] = [];
  readyState = 0;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  constructor(public url: string) {
    FakeWS.all.push(this);
  }
  send(d: string) {
    this.sent.push(d);
  }
  close() {
    if (this.readyState === 3) return;
    this.readyState = 3;
    this.onclose?.();
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  drop() {
    this.readyState = 3;
    this.onclose?.();
  }
}

class NoopObserver {
  observe() {}
  disconnect() {}
}

async function openLive() {
  render(BrowserPanel, { props: { onError: vi.fn() } });
  const sel = (await screen.findByTestId("browser-session")) as HTMLSelectElement;
  sel.value = "s1";
  await fireEvent.change(sel);
  await waitFor(() => expect(FakeWS.all.length).toBe(1));
  FakeWS.all[0].open();
  await waitFor(() => expect(document.body.textContent).toContain("live"));
}

describe("BrowserPanel reconnect", () => {
  beforeEach(() => {
    FakeWS.all = [];
    vi.stubGlobal("WebSocket", FakeWS);
    vi.stubGlobal("ResizeObserver", NoopObserver);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  test("a dropped socket reconnects on its own", async () => {
    await openLive();
    vi.useFakeTimers();
    FakeWS.all[0].drop();
    await vi.waitFor(() => expect(document.body.textContent).toContain("reconnecting"));
    await vi.advanceTimersByTimeAsync(1100);
    expect(FakeWS.all.length).toBe(2);
    FakeWS.all[1].open();
    await vi.waitFor(() => expect(screen.queryByTestId("browser-reconnect")).toBeNull());
  });

  test("after the retries run out, a click reconnects", async () => {
    await openLive();
    vi.useFakeTimers();
    FakeWS.all[0].drop();
    for (let i = 0; i < 8; i++) {
      await vi.advanceTimersByTimeAsync(16000);
      FakeWS.all[FakeWS.all.length - 1].drop();
    }
    await vi.advanceTimersByTimeAsync(16000);
    const tries = FakeWS.all.length;
    expect(tries).toBe(9); // first socket + 8 retries, then it stops
    const btn = await vi.waitFor(() => screen.getByTestId("browser-reconnect"));
    expect(btn.textContent).toContain("disconnected");
    await fireEvent.click(btn);
    expect(FakeWS.all.length).toBe(tries + 1);
  });

  test("a socket that stops answering is dropped and reopened", async () => {
    await openLive();
    vi.useFakeTimers();
    // Reopen under fake timers so the heartbeat runs on them.
    FakeWS.all[0].drop();
    await vi.advanceTimersByTimeAsync(1100);
    FakeWS.all[1].open();
    // No replies to the heartbeat: two missed beats (15s each + 3s timeout).
    await vi.advanceTimersByTimeAsync(15000 * 2 + 3500);
    expect(FakeWS.all[1].readyState).toBe(3);
    await vi.advanceTimersByTimeAsync(1100);
    expect(FakeWS.all.length).toBe(3);
  });

  test("leaving the session does not reconnect", async () => {
    await openLive();
    vi.useFakeTimers();
    const sel = screen.getByTestId("browser-session") as HTMLSelectElement;
    sel.value = "";
    await fireEvent.change(sel);
    await vi.advanceTimersByTimeAsync(20000);
    expect(FakeWS.all.length).toBe(1);
  });
});
