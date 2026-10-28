import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { ListenState } from "../../api/listen.js";

let state: ListenState | null;
const setListen = vi.fn((_b: string, _s: string, action: "on" | "off", instructions?: string) => {
  state = { ...(state as ListenState), listening: action === "on", thread_prompt: instructions ?? "" };
  return Promise.resolve(state);
});

vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  runApi: <T,>(p: Promise<T>) => p,
}));
vi.mock("../../api/listen.js", async (orig) => ({
  ...(await orig<typeof import("../../api/listen.js")>()),
  getListen: () => (state ? Promise.resolve(state) : Promise.reject(new Error("not a Slack thread"))),
  setListen: (b: string, s: string, a: "on" | "off", i?: string) => setListen(b, s, a, i),
}));

import ListenChip from "../ListenChip.svelte";

const base: ListenState = { listening: true, channel_id: "C1", thread_ts: "1.1", delay_sec: 60, scope: "thread", on_team_reply: "review", instructions: "Answer only when…" };

describe("ListenChip", () => {
  test("renders nothing for a session that is no Slack thread", async () => {
    state = null;
    render(ListenChip, { props: { base: "/b", sessionId: "S" } });
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByTestId("listen-chip")).toBeNull();
  });

  test("shows Listening and stops from the panel", async () => {
    state = { ...base };
    render(ListenChip, { props: { base: "/b", sessionId: "S" } });
    const chip = await screen.findByText(/Listening/);
    await fireEvent.click(chip);
    expect(screen.getByTestId("listen-panel").textContent).toContain("Delay 1m");
    await fireEvent.click(screen.getByText("Stop listening"));
    await waitFor(() => expect(screen.getByTestId("listen-chip").dataset.listening).toBe("false"));
    expect(setListen).toHaveBeenLastCalledWith("/b", "S", "off", undefined);
  });

  test("starts listening with this thread's instructions", async () => {
    state = { ...base, listening: false };
    render(ListenChip, { props: { base: "/b", sessionId: "S" } });
    await fireEvent.click(await screen.findByText(/Off/));
    const box = screen.getByPlaceholderText("e.g. only WABA questions") as HTMLTextAreaElement;
    await fireEvent.input(box, { target: { value: "only WABA" } });
    await fireEvent.click(screen.getByText("Start listening"));
    await waitFor(() => expect(setListen).toHaveBeenLastCalledWith("/b", "S", "on", "only WABA"));
  });
});
