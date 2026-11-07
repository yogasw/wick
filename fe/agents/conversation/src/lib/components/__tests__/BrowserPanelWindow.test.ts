import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { Effect } from "effect";

const closed: string[] = [];

vi.mock("../../api/browser.js", async (orig) => ({
  ...(await orig<typeof import("../../api/browser.js")>()),
  listInstances: () => Effect.succeed([{ id: "c1", label: "Browser", disabled: false }]),
  listSessions: () =>
    Effect.succeed({
      sessions: closed.includes("s1")
        ? []
        : [
            {
              session_id: "s1",
              pid: 1,
              browser: "chromium",
              created: "",
              profile: "test-profile",
              idle_seconds: 125,
              auto_close_in_seconds: 300,
              tabs: [{ index: 0, url: "https://example.com/inbox", title: "Inbox" }],
            },
          ],
      maxTabs: 1,
    }),
  closeSession: (_c: string, sid: string) => {
    closed.push(sid);
    return Effect.succeed({ session_id: sid, closed: true });
  },
  wsURL: () => "ws://test/cdp",
}));

import BrowserPanel from "../BrowserPanel.svelte";
import TwoBrowserPanels from "./TwoBrowserPanels.svelte";

class FakeWS {
  static OPEN = 1;
  readyState = 0;
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  constructor(public url: string) {}
  send() {}
  close() {
    this.readyState = 3;
    this.onclose?.();
  }
}

class NoopObserver {
  observe() {}
  disconnect() {}
}

describe("BrowserPanel session list and window modes", () => {
  beforeEach(() => {
    closed.length = 0;
    vi.stubGlobal("WebSocket", FakeWS);
    vi.stubGlobal("ResizeObserver", NoopObserver);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test("an instance lists its sessions as cards with profile, host and idle clock", async () => {
    render(BrowserPanel, { props: { onError: vi.fn() } });
    const card = await screen.findByTestId("browser-session-card");
    expect(card.textContent).toContain("test-profile");
    expect(card.textContent).toContain("example.com");
    expect(card.textContent).toContain("idle 2 min");
    expect(card.textContent).toContain("closes in 5 min");
    expect(screen.queryByTestId("browser-session")).toBeNull(); // no dropdown any more
  });

  test("open shows the viewer in the panel, back returns to the list", async () => {
    render(BrowserPanel, { props: { onError: vi.fn() } });
    await fireEvent.click(await screen.findByTestId("browser-session-open"));
    expect(screen.getByTestId("browser-back")).toBeTruthy();
    expect(screen.queryByTestId("browser-session-card")).toBeNull();
    await fireEvent.click(screen.getByTestId("browser-back"));
    expect(await screen.findByTestId("browser-session-card")).toBeTruthy();
  });

  test("pop out, fullscreen and back: fullscreen returns to where it came from", async () => {
    render(BrowserPanel, { props: { onError: vi.fn() } });
    await fireEvent.click(await screen.findByTestId("browser-session-open"));

    await fireEvent.click(screen.getByTestId("browser-float"));
    expect(screen.getByTestId("browser-overlay")).toBeTruthy();
    expect(screen.queryByTestId("browser-back")).toBeNull(); // the panel no longer holds the viewer

    await fireEvent.click(screen.getByTestId("browser-max"));
    await fireEvent.click(screen.getByTestId("browser-exit-max"));
    expect(screen.getByTestId("browser-dock")).toBeTruthy(); // back in the floating window

    await fireEvent.click(screen.getByTestId("browser-dock"));
    expect(screen.queryByTestId("browser-overlay")).toBeNull();
    expect(screen.getByTestId("browser-back")).toBeTruthy();
  });

  test("the card opens on click and closes from its ⋯ menu", async () => {
    render(BrowserPanel, { props: { onError: vi.fn() } });
    await screen.findByTestId("browser-session-card");
    expect(screen.queryByText("Open")).toBeNull(); // no separate Open button
    await fireEvent.click(screen.getByTestId("browser-session-more"));
    await fireEvent.click(screen.getByTestId("browser-session-close"));
    await waitFor(() => expect(screen.queryByTestId("browser-session-card")).toBeNull());
    expect(closed).toEqual(["s1"]);
  });

  test("a second panel without a session does not undo pop out or fullscreen", async () => {
    render(TwoBrowserPanels);
    const opens = await screen.findAllByTestId("browser-session-open");
    await fireEvent.click(opens[0]);
    await fireEvent.click(screen.getByTestId("browser-float"));
    await waitFor(() => expect(screen.getByTestId("shared-view").textContent).toBe("float"));
    expect(screen.getByTestId("browser-overlay")).toBeTruthy();
    await fireEvent.click(screen.getByTestId("browser-max"));
    await waitFor(() => expect(screen.getByTestId("shared-view").textContent).toBe("max"));
    expect(screen.getByTestId("browser-overlay")).toBeTruthy();
  });

  test("closing the session from the floating window docks back to the list", async () => {
    render(BrowserPanel, { props: { onError: vi.fn() } });
    await fireEvent.click(await screen.findByTestId("browser-session-open"));
    await fireEvent.click(screen.getByTestId("browser-float"));
    await fireEvent.click(screen.getByTestId("browser-more"));
    await fireEvent.click(screen.getByTestId("browser-close"));
    await waitFor(() => expect(screen.queryByTestId("browser-overlay")).toBeNull());
    expect(closed).toEqual(["s1"]);
  });
});
