import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ThreadMessage from "../ThreadMessage.svelte";
import type { ConversationTurn, TurnEvent } from "../../types/agents.js";

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

describe("ThreadMessage - null-safe backend arrays (Go nil → JSON null)", () => {
  test("renders user turn without crash when events and attachments are null (Go nil slice)", () => {
    const turn = makeTurn({
      role: "user",
      text: "hi",
      events: undefined as any,
      attachments: undefined as any,
    });
    expect(() => render(ThreadMessage, { props: { turn } })).not.toThrow();
  });

  test("renders assistant turn without crash when events is null (Go nil slice)", () => {
    const turn = makeTurn({
      role: "assistant",
      text: "hello",
      events: null as any,
      attachments: null as any,
    });
    expect(() => render(ThreadMessage, { props: { turn } })).not.toThrow();
  });
});

describe("ThreadMessage - show trace toggle", () => {
  test("assistant turn with has_trace:true and loadTrace prop renders show trace toggle", () => {
    const turn = makeTurn({ role: "assistant", has_trace: true });
    const loadTrace = vi.fn().mockResolvedValue([]);
    render(ThreadMessage, { props: { turn, loadTrace } });
    expect(screen.getByText(/show trace/i)).toBeDefined();
  });

  test("user turn does NOT render show trace toggle even if has_trace:true", () => {
    const turn = makeTurn({ role: "user", has_trace: true });
    const loadTrace = vi.fn().mockResolvedValue([]);
    const { container } = render(ThreadMessage, { props: { turn, loadTrace } });
    expect(container.innerHTML).not.toContain("show trace");
  });

  test("assistant turn with has_trace:false does NOT render show trace toggle", () => {
    const turn = makeTurn({ role: "assistant", has_trace: false });
    const loadTrace = vi.fn().mockResolvedValue([]);
    const { container } = render(ThreadMessage, { props: { turn, loadTrace } });
    expect(container.innerHTML).not.toContain("show trace");
  });

  test("assistant turn with has_trace:true but no loadTrace does NOT render show trace toggle", () => {
    const turn = makeTurn({ role: "assistant", has_trace: true });
    const { container } = render(ThreadMessage, { props: { turn } });
    expect(container.innerHTML).not.toContain("show trace");
  });

  test("clicking show trace calls loadTrace with turn_id and flips label to hide trace", async () => {
    const traceEvents: TurnEvent[] = [{ type: "thinking", text: "reasoning here" }];
    const loadTrace = vi.fn().mockResolvedValue(traceEvents);
    const turn = makeTurn({ role: "assistant", has_trace: true, turn_id: "t-trace-1" });

    render(ThreadMessage, { props: { turn, loadTrace } });

    const btn = screen.getByText(/show trace/i).closest("button")!;
    await fireEvent.click(btn);

    await vi.waitFor(() => {
      expect(screen.getByText(/hide trace/i)).toBeDefined();
    });

    expect(loadTrace).toHaveBeenCalledOnce();
    expect(loadTrace).toHaveBeenCalledWith("t-trace-1");
  });

  test("after expand, thinking event text is rendered in the trace section", async () => {
    const traceEvents: TurnEvent[] = [{ type: "thinking", text: "deep thoughts" }];
    const loadTrace = vi.fn().mockResolvedValue(traceEvents);
    const turn = makeTurn({ role: "assistant", has_trace: true });

    render(ThreadMessage, { props: { turn, loadTrace } });

    const btn = screen.getByText(/show trace/i).closest("button")!;
    await fireEvent.click(btn);

    await vi.waitFor(() => {
      expect(screen.getByText("deep thoughts")).toBeDefined();
    });
  });

  test("after expand with tool_use+tool_result events, ToolCard is rendered (tool name visible)", async () => {
    const traceEvents: TurnEvent[] = [
      { type: "tool_use", tool_use_id: "tu-t1", tool_name: "read_file", tool_input: '{"path":"/tmp/x"}' },
      { type: "tool_result", tool_use_id: "tu-t1", text: "file contents", is_error: false },
    ];
    const loadTrace = vi.fn().mockResolvedValue(traceEvents);
    const turn = makeTurn({ role: "assistant", has_trace: true });

    render(ThreadMessage, { props: { turn, loadTrace } });

    const btn = screen.getByText(/show trace/i).closest("button")!;
    await fireEvent.click(btn);

    await vi.waitFor(() => {
      expect(screen.getAllByText("read_file").length).toBeGreaterThan(0);
    });
  });

  test("clicking hide trace hides the section without refetching loadTrace", async () => {
    const traceEvents: TurnEvent[] = [{ type: "thinking", text: "cached thought" }];
    const loadTrace = vi.fn().mockResolvedValue(traceEvents);
    const turn = makeTurn({ role: "assistant", has_trace: true });

    render(ThreadMessage, { props: { turn, loadTrace } });

    const showBtn = screen.getByText(/show trace/i).closest("button")!;
    await fireEvent.click(showBtn);

    await vi.waitFor(() => {
      expect(screen.getByText(/hide trace/i)).toBeDefined();
    });

    const hideBtn = screen.getByText(/hide trace/i).closest("button")!;
    await fireEvent.click(hideBtn);

    await vi.waitFor(() => {
      expect(screen.getByText(/show trace/i)).toBeDefined();
    });

    expect(loadTrace).toHaveBeenCalledOnce();
  });

  test("loadTrace rejection shows failed to load trace error message", async () => {
    const loadTrace = vi.fn().mockRejectedValue(new Error("network error"));
    const turn = makeTurn({ role: "assistant", has_trace: true });

    render(ThreadMessage, { props: { turn, loadTrace } });

    const btn = screen.getByText(/show trace/i).closest("button")!;
    await fireEvent.click(btn);

    await vi.waitFor(() => {
      expect(screen.getByText(/failed to load trace/i)).toBeDefined();
    });
  });
});
