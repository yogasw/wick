import { describe, test, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ThreadMessage from "../ThreadMessage.svelte";
import type { ConversationTurn } from "../../types/agents.js";

const turn = (o: Partial<ConversationTurn> = {}): ConversationTurn => ({
  turn_id: "t-1", role: "user", agent: "main", provider: "p", text: "hi", timestamp: 0, ts: "2026-01-02T03:04:05Z",
  truncated: false, interrupted: false, has_trace: false, events: [], attachments: [], ...o,
}) as ConversationTurn;

describe("held Slack reply", () => {
  test("no listen chip; the timestamp carries the hold", async () => {
    render(ThreadMessage, {
      props: {
        turn: turn({ text: "[auto-reply · held 3s] Not a mention, but this thread auto-replies.\n\ncek dong" }),
        listenHold: { mode: "auto_reply", state: "sent", delaySec: 3, startedMs: Date.parse("2026-01-02T03:04:02Z"), heldFor: "3s", msgTs: "" },
      },
    });
    expect(screen.queryByTestId("listen-note-chip")).toBeNull();
    expect(screen.queryByText(/auto-reply ·/)).toBeNull();
    await fireEvent.mouseEnter(screen.getByTestId("held-stamp"));
    const card = screen.getByTestId("held-card").textContent ?? "";
    expect(card).toContain("Auto-reply · this message");
    expect(card).toContain("Received");
    expect(card).toMatch(/Held for\s*3s/);
    expect(card).toContain("Passed to the agent");
    expect(card).toMatch(/Via\s*Slack/);
  });

  test("an older turn with only the note still gets the card", async () => {
    render(ThreadMessage, { props: { turn: turn({ text: "[listen mode · held 15s] Not a mention.\n\nhalo" }) } });
    await fireEvent.mouseEnter(screen.getByTestId("held-stamp"));
    expect(screen.getByTestId("held-card").textContent).toContain("Listen · this message");
    expect(screen.getByTestId("held-card").textContent).toContain("15s");
  });

  test("a plain message has a plain timestamp", () => {
    render(ThreadMessage, { props: { turn: turn() } });
    expect(screen.queryByTestId("held-stamp")).toBeNull();
  });

  test("a hold that never reached the agent is a red row with its reason", () => {
    render(ThreadMessage, {
      props: { turn: turn({ role: "system", kind: "listen_held", text: "Listen · not passed on: someone else answered", extras: { hold_id: "S/1", state: "skipped", mode: "listen" } }) },
    });
    const row = screen.getByTestId("listen-problem");
    expect(row.textContent).toContain("Listen: Not passed to the agent: someone else answered");
    expect(row.querySelector("button")).toBeNull();
  });
});
