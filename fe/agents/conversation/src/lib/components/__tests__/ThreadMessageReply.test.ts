import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ThreadMessage from "../ThreadMessage.svelte";
import type { ConversationTurn } from "../../types/agents.js";

function makeTurn(overrides: Partial<ConversationTurn> = {}): ConversationTurn {
  return {
    turn_id: "111",
    role: "assistant",
    agent: "main",
    provider: "anthropic/claude",
    text: "Deploy finished",
    timestamp: 0,
    truncated: false,
    interrupted: false,
    has_trace: false,
    events: [],
    attachments: [],
    ...overrides,
  };
}

describe("ThreadMessage - Reply action", () => {
  test("shown with onReply and hands the target over", async () => {
    const onReply = vi.fn();
    render(ThreadMessage, { props: { turn: makeTurn(), onReply } });
    const btn = screen.getByTestId("reply-action");
    expect(btn.getAttribute("aria-label")).toBe("Reply to this message");
    await fireEvent.click(btn);
    expect(onReply).toHaveBeenCalledWith({ turnId: "111", author: "main", excerpt: "Deploy finished" });
  });

  test("user bubble can be replied to as well", async () => {
    const onReply = vi.fn();
    render(ThreadMessage, { props: { turn: makeTurn({ role: "user", turn_id: "turn-0", text: "is it\ndone?" }), onReply } });
    await fireEvent.click(screen.getByTestId("reply-action"));
    expect(onReply).toHaveBeenCalledWith({ turnId: "turn-0", author: "You", excerpt: "is it done?" });
  });

  test("absent for a read-only viewer (no onReply)", () => {
    render(ThreadMessage, { props: { turn: makeTurn() } });
    expect(screen.queryByTestId("reply-action")).toBeNull();
  });

  test("absent on an optimistic bubble the server has not stored yet", () => {
    render(ThreadMessage, { props: { turn: makeTurn({ role: "user", turn_id: "local-user-1" }), onReply: vi.fn() } });
    expect(screen.queryByTestId("reply-action")).toBeNull();
  });
});

describe("ThreadMessage - quote preview", () => {
  const replyTurn = () =>
    makeTurn({
      role: "user",
      turn_id: "222",
      text: "roll it back",
      reply_to: { turn_id: "111", role: "assistant", author: "@ops", excerpt: "x".repeat(300) },
    });

  test("renders author and a one-line clipped excerpt above the text", () => {
    render(ThreadMessage, { props: { turn: replyTurn() } });
    const q = screen.getByTestId("reply-quote");
    expect(q.textContent).toContain("@ops");
    expect(q.textContent).toContain("…");
    expect(q.textContent!.length).toBeLessThan(140);
    expect(screen.getByText("roll it back")).toBeDefined();
  });

  test("clicking it jumps to the original", async () => {
    const onJumpToTurn = vi.fn();
    render(ThreadMessage, { props: { turn: replyTurn(), onJumpToTurn } });
    await fireEvent.click(screen.getByTestId("reply-quote"));
    expect(onJumpToTurn).toHaveBeenCalledWith("111");
  });

  test("an old turn without reply_to renders no quote", () => {
    render(ThreadMessage, { props: { turn: makeTurn({ role: "user", text: "hi" }) } });
    expect(screen.queryByTestId("reply-quote")).toBeNull();
  });
});
