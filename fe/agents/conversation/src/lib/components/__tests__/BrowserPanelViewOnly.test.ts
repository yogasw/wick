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

// The panel shows the agent's point of view: in no mode may it resize the
// remote viewport, change its colour scheme or run page scripts on its own.
const changesTheAgentsView = (m: { method: string; params?: any }) =>
  m.method.startsWith("Emulation.") ||
  (m.method === "Runtime.evaluate" && m.params?.expression !== "1");

function sentMsgs(ws: FakeWS): { method: string; params?: any }[] {
  return ws.sent.map((d) => JSON.parse(d));
}

describe("BrowserPanel keeps the agent's view", () => {
  beforeEach(() => {
    FakeWS.all = [];
    vi.stubGlobal("WebSocket", FakeWS);
    vi.stubGlobal("ResizeObserver", NoopObserver);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test("view only streams and sends nothing that changes the page", async () => {
    await openLive();
    await waitFor(() => expect(sentMsgs(FakeWS.all[0]).map((m) => m.method)).toContain("Page.startScreencast"));
    const sent = sentMsgs(FakeWS.all[0]);
    expect(sent.filter(changesTheAgentsView)).toEqual([]);
    expect(sent.filter((m) => m.method.startsWith("Input."))).toEqual([]);
  });

  test("switching to full reopens the socket and still never emulates", async () => {
    await openLive();
    await fireEvent.click(screen.getByTestId("browser-mode-full"));
    await waitFor(() => expect(FakeWS.all.length).toBe(2));
    FakeWS.all[1].open();
    await waitFor(() => expect(sentMsgs(FakeWS.all[1]).map((m) => m.method)).toContain("Page.startScreencast"));
    expect(sentMsgs(FakeWS.all[1]).filter(changesTheAgentsView)).toEqual([]);
  });
});
