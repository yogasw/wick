import { describe, test, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ThreadMessage from "../ThreadMessage.svelte";
import { setViewerId } from "../../viewer.js";
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

describe("listening thread", () => {
  test("a heard reply shows who said it and that it is not read yet", () => {
    render(ThreadMessage, {
      props: {
        turn: turn({ role: "system", kind: "thread_heard", text: "kenapa form-nya kosong?", extras: { hold_id: "S/1", name: "Dev Example (@dev)", files: "shot.png" } }),
        heardStatus: "unread",
      },
    });
    const row = screen.getByTestId("heard-message");
    expect(row.textContent).toContain("Dev Example");
    expect(row.textContent).not.toContain("(@dev)");
    expect(row.textContent).toContain("kenapa form-nya kosong?");
    expect(row.textContent).toContain("shot.png");
    expect(row.textContent).toContain("not read yet");
  });

  test("a waiting hold counts down to the read", () => {
    render(ThreadMessage, {
      props: {
        turn: turn({ role: "system", kind: "listen_held", text: "Listen · holding", extras: { hold_id: "S/1", state: "held", mode: "listen", delay_sec: "120", started_ms: String(Date.now()), deadline_ms: String(Date.now() + 90_000), heard: "3" } }),
        agent: { name: "Helper" },
      },
    });
    const row = screen.getByTestId("listen-countdown").textContent ?? "";
    expect(row).toContain("Helper heard 3 new replies");
    expect(row).toMatch(/reads in 1:[23]\d/);
  });

  test("the turn a hold became is a read line once its replies are in the thread", async () => {
    render(ThreadMessage, {
      props: {
        turn: turn({ text: "[listen mode · held 2m0s] Not a mention.\n\nkadang juga ada" }),
        listenHold: { mode: "listen", state: "sent", delaySec: 120, startedMs: 0, heldFor: "2m0s", msgTs: "", heard: 4 },
        agent: { name: "Helper" },
      },
    });
    expect(screen.getByTestId("listen-read-line").textContent).toContain("✓ Helper read 4 replies · held 2m0s");
    await fireEvent.click(screen.getByText("Show prompt"));
    expect(screen.queryByTestId("listen-read-line")).toBeNull();
  });

  test("the reader's own heard reply sits on the right, without a name", () => {
    setViewerId("wick-user-1");
    render(ThreadMessage, {
      props: {
        turn: turn({ role: "system", kind: "thread_heard", text: "cek https://example.com/x", extras: { hold_id: "S/1", user: "U01TEST", name: "Dev Example", wick_user_id: "wick-user-1" } }),
        heardStatus: "read",
        heardRun: { first: true, last: true },
      },
    });
    const row = screen.getByTestId("heard-message");
    expect(row.dataset.mine).toBe("1");
    expect(row.textContent).not.toContain("Dev Example");
    expect(row.querySelector("a")?.getAttribute("href")).toBe("https://example.com/x");
    setViewerId("");
  });

  test("a run by one author shows the name once and the read state once", () => {
    const heard = (o: { first: boolean; last: boolean }) =>
      render(ThreadMessage, {
        props: {
          turn: turn({ role: "system", kind: "thread_heard", text: "satu", extras: { hold_id: "S/1", user: "U02TEST", name: "Other Example" } }),
          heardStatus: "unread",
          heardRun: o,
        },
      }).container.textContent ?? "";
    const top = heard({ first: true, last: false });
    expect(top).toContain("Other Example");
    expect(top).not.toContain("not read yet");
    const bottom = heard({ first: false, last: true });
    expect(bottom).not.toContain("Other Example");
    expect(bottom).toContain("not read yet");
  });
});
