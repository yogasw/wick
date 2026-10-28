import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { ListenState, ReplyMode } from "../../api/listen.js";

let state: ListenState | null;
const setReplyMode = vi.fn((_b: string, _s: string, mode: ReplyMode) => {
  state = { ...(state as ListenState), mode, listening: mode !== "manual" };
  return Promise.resolve(state);
});

vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  runApi: <T,>(p: Promise<T>) => p,
}));
vi.mock("../../api/listen.js", async (orig) => ({
  ...(await orig<typeof import("../../api/listen.js")>()),
  getListen: () => (state ? Promise.resolve(state) : Promise.reject(new Error("not a Slack thread"))),
  setReplyMode: (b: string, s: string, m: ReplyMode) => setReplyMode(b, s, m),
}));

import ListenModeButton from "../ListenModeButton.svelte";

const base: ListenState = {
  listening: true, channel_id: "C01TESTCHAN", thread_ts: "1.1", delay_sec: 60, scope: "thread", on_team_reply: "review",
  mode: "auto_reply", channel_mode: "auto_reply", channel_name: "general", hold_sec: 3,
};

describe("ListenModeButton", () => {
  test("hidden for a session that is no Slack thread", async () => {
    state = null;
    render(ListenModeButton, { props: { base: "/b", sessionId: "S" } });
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByTestId("listen-mode")).toBeNull();
  });

  test.each([
    ["auto_reply", true],
    ["listen", true],
    ["manual", false],
  ] as const)("dot for %s: %s", async (mode, dot) => {
    state = { ...base, mode, listening: mode !== "manual" };
    render(ListenModeButton, { props: { base: "/b", sessionId: "S" } });
    await screen.findByTestId("listen-mode");
    expect(!!screen.queryByTestId("listen-mode-dot")).toBe(dot);
  });

  test("hover card, then the menu switches the mode", async () => {
    state = { ...base };
    render(ListenModeButton, { props: { base: "/b", sessionId: "S" } });
    const btn = await screen.findByLabelText("Reply mode");
    await fireEvent.mouseEnter(btn);
    const card = screen.getByTestId("listen-mode-card").textContent ?? "";
    expect(card).toContain("Auto-reply is on in this thread");
    expect(card).toContain("Slack · #general");
    expect(card).toMatch(/Holds messages\s*3s/);
    expect(card).toContain("Click to change");
    await fireEvent.click(btn);
    const menu = screen.getByTestId("listen-mode-menu");
    expect(menu.textContent).toContain("Replies in #general");
    expect(screen.getByRole("menuitemradio", { checked: true }).textContent).toContain("Auto-reply");
    await fireEvent.click(screen.getByRole("menuitemradio", { name: /Manual/ }));
    expect(setReplyMode).toHaveBeenCalledWith("/b", "S", "manual");
    await waitFor(() => expect(screen.getByTestId("listen-mode").dataset.mode).toBe("manual"));
    expect(screen.queryByTestId("listen-mode-dot")).toBeNull();
  });
});
