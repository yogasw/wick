import { describe, test, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";
import ThreadMessage from "../ThreadMessage.svelte";
import type { ConversationTurn } from "../../types/agents.js";

function makeTurn(overrides: Partial<ConversationTurn> = {}): ConversationTurn {
  return {
    turn_id: "t-001",
    role: "user",
    agent: "main",
    provider: "anthropic/claude-sonnet",
    text: "Hello world",
    timestamp: 0,
    truncated: false,
    interrupted: false,
    has_trace: false,
    events: [],
    attachments: [],
    ...overrides,
  };
}

describe("ThreadMessage - user turn", () => {
  test("renders user text", () => {
    render(ThreadMessage, { props: { turn: makeTurn({ role: "user", text: "Hello world" }) } });
    expect(screen.getByText("Hello world")).toBeDefined();
  });

  test("user bubble is right-aligned (justify-end)", () => {
    const { container } = render(ThreadMessage, { props: { turn: makeTurn({ role: "user", text: "Hi" }) } });
    expect(container.innerHTML).toContain("justify-end");
  });
});

describe("ThreadMessage - assistant turn", () => {
  test("renders assistant text as markdown (bullet becomes li)", () => {
    const { container } = render(ThreadMessage, {
      props: { turn: makeTurn({ role: "assistant", text: "- bullet item" }) },
    });
    expect(container.innerHTML).toContain("<li");
    expect(container.innerHTML).toContain("bullet item");
  });

  test("assistant bubble is left-aligned (justify-start)", () => {
    const { container } = render(ThreadMessage, {
      props: { turn: makeTurn({ role: "assistant", text: "Hello" }) },
    });
    expect(container.innerHTML).toContain("justify-start");
  });

  test("turn with tool_use event renders ToolCard (tool name visible)", () => {
    const turn = makeTurn({
      role: "assistant",
      text: "I ran bash",
      events: [
        { type: "tool_use", tool_use_id: "tu-1", tool_name: "bash", tool_input: '{"cmd":"ls"}' },
        { type: "tool_result", tool_use_id: "tu-1", text: "file.txt", is_error: false },
      ],
    });
    render(ThreadMessage, { props: { turn } });
    expect(screen.getByText("bash")).toBeDefined();
  });
});
