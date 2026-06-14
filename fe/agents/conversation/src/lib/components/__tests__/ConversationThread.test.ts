import { describe, test, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";
import ConversationThread from "../ConversationThread.svelte";
import type { ConversationTurn, LiveTurn, TypingState } from "../../types/agents.js";

function makeTurn(id: string, role: string, text: string): ConversationTurn {
  return {
    turn_id: id,
    role,
    agent: "main",
    provider: "anthropic/claude-sonnet",
    text,
    timestamp: 0,
    truncated: false,
    interrupted: false,
    has_trace: false,
    events: [],
    attachments: [],
  };
}

const TURN_A = makeTurn("t-1", "user", "Hello from user");
const TURN_B = makeTurn("t-2", "assistant", "Hello from assistant");
const TURN_C = makeTurn("t-3", "user", "Another message");

describe("ConversationThread", () => {
  test("renders all historical turns", () => {
    render(ConversationThread, {
      props: {
        turns: [TURN_A, TURN_B, TURN_C],
        live: null,
        typing: { active: false },
      },
    });
    expect(screen.getByText("Hello from user")).toBeDefined();
    expect(screen.getByText("Another message")).toBeDefined();
  });

  test("renders live turn text", () => {
    const live: LiveTurn = { text: "streaming text here", blocks: [] };
    render(ConversationThread, {
      props: { turns: [], live, typing: { active: false } },
    });
    expect(screen.getByText("streaming text here")).toBeDefined();
  });

  test("shows typing indicator when typing.active is true", () => {
    const typing: TypingState = { active: true, substate: "thinking" };
    const { container } = render(ConversationThread, {
      props: { turns: [], live: null, typing },
    });
    expect(container.innerHTML).toContain("thinking…");
  });

  test("hides typing indicator when typing.active is false", () => {
    const { container } = render(ConversationThread, {
      props: { turns: [], live: null, typing: { active: false } },
    });
    expect(container.innerHTML).not.toContain("thinking…");
    expect(container.innerHTML).not.toContain("animate-spin");
  });

  test("typing indicator shows substate when provided", () => {
    const typing: TypingState = { active: true, substate: "bash" };
    const { container } = render(ConversationThread, {
      props: { turns: [], live: null, typing },
    });
    expect(container.innerHTML).toContain("running bash…");
  });
});
