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

  test("turn with tool_use event shows trace toggle (tool card is inside trace, not bubble)", async () => {
    const turn = makeTurn({
      role: "assistant",
      text: "I ran bash",
      events: [
        { type: "tool_use", tool_use_id: "tu-1", tool_name: "bash", tool_input: '{"cmd":"ls"}' },
        { type: "tool_result", tool_use_id: "tu-1", text: "file.txt", is_error: false },
      ],
    });
    render(ThreadMessage, { props: { turn } });
    expect(screen.getByText(/show trace/i)).toBeDefined();
    const btn = screen.getByText(/show trace/i).closest("button")!;
    await fireEvent.click(btn);
    await vi.waitFor(() => {
      expect(screen.getByText("bash")).toBeDefined();
    });
  });
});

describe("ThreadMessage - system turn", () => {
  test("system turn renders centered pill (justify-center), not an assistant bubble", () => {
    const turn = makeTurn({ role: "system", turn_id: "sys-1", text: "Switched provider to claude" });
    const { container } = render(ThreadMessage, { props: { turn } });
    expect(container.innerHTML).toContain("justify-center");
    expect(container.innerHTML).not.toContain("justify-start");
  });

  test("system turn shows pill text", () => {
    render(ThreadMessage, { props: { turn: makeTurn({ role: "system", turn_id: "sys-2", text: "Project moved" }) } });
    expect(screen.getByText("Project moved")).toBeDefined();
  });

  test("system turn renders step events as a step list", () => {
    const turn = makeTurn({
      role: "system", turn_id: "sys-3", text: "Done",
      events: [{ type: "step", text: "cloned repo" }, { type: "step", text: "ran setup" }],
    });
    render(ThreadMessage, { props: { turn } });
    expect(screen.getByText("cloned repo")).toBeDefined();
    expect(screen.getByText("ran setup")).toBeDefined();
  });

  test("system turn does NOT render a show-trace toggle", () => {
    const turn = makeTurn({ role: "system", turn_id: "sys-4", text: "x", has_trace: true });
    const loadTrace = vi.fn().mockResolvedValue([]);
    const { container } = render(ThreadMessage, { props: { turn, loadTrace } });
    expect(container.innerHTML).not.toContain("show trace");
  });
});

describe("ThreadMessage - attachments", () => {
  test("image attachment renders inline <img> thumbnail", () => {
    const turn = makeTurn({ role: "user", text: "", attachments: [{ name: "p.png", stored_name: "p.png", url: "/u/p.png", mime: "image/png", size: 10 }] });
    const { container } = render(ThreadMessage, { props: { turn } });
    const img = container.querySelector("img");
    expect(img).not.toBeNull();
    expect(img!.getAttribute("src")).toBe("/u/p.png");
  });

  test("non-image attachment renders a file-row chip (no <img>)", () => {
    const turn = makeTurn({ role: "user", text: "", attachments: [{ name: "a.pdf", stored_name: "a.pdf", url: "/u/a.pdf", mime: "application/pdf", size: 10 }] });
    const { container } = render(ThreadMessage, { props: { turn } });
    expect(container.querySelector("img")).toBeNull();
    expect(screen.getByText("a.pdf")).toBeDefined();
  });
});

describe("ThreadMessage - interrupted fallback", () => {
  test("interrupted assistant turn with no text renders an interrupted fallback bubble", () => {
    const turn = makeTurn({ role: "assistant", text: "", interrupted: true });
    render(ThreadMessage, { props: { turn } });
    expect(screen.getByText(/interrupted/i)).toBeDefined();
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

  test("assistant turn with has_trace:false and no events does NOT render show trace toggle", () => {
    const turn = makeTurn({ role: "assistant", has_trace: false, events: [] });
    const loadTrace = vi.fn().mockResolvedValue([]);
    const { container } = render(ThreadMessage, { props: { turn, loadTrace } });
    expect(container.innerHTML).not.toContain("show trace");
  });

  test("assistant turn with has_trace:true and no loadTrace DOES render show trace toggle (local events path)", () => {
    const turn = makeTurn({ role: "assistant", has_trace: true, events: [] });
    render(ThreadMessage, { props: { turn } });
    expect(screen.getByText(/show trace/i)).toBeDefined();
  });

  test("assistant turn with events only (has_trace:false, no loadTrace) DOES render show trace toggle", () => {
    const turn = makeTurn({
      role: "assistant",
      has_trace: false,
      events: [{ type: "thinking", text: "thoughts" }],
    });
    const { container } = render(ThreadMessage, { props: { turn } });
    expect(container.innerHTML).toContain("show trace");
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

  test("expanding events-only turn (no loadTrace) renders thinking text without fetch", async () => {
    const turn = makeTurn({
      role: "assistant",
      has_trace: false,
      events: [{ type: "thinking", text: "inner thoughts" }],
    });

    render(ThreadMessage, { props: { turn } });

    const btn = screen.getByText(/show trace/i).closest("button")!;
    await fireEvent.click(btn);

    await vi.waitFor(() => {
      expect(screen.getByText("inner thoughts")).toBeDefined();
    });
  });

  test("synthetic turn_id starting with 'live-' does NOT call loadTrace on expand", async () => {
    const localEvents: TurnEvent[] = [{ type: "thinking", text: "local thought" }];
    const loadTrace = vi.fn().mockResolvedValue([]);
    const turn = makeTurn({
      role: "assistant",
      has_trace: true,
      turn_id: "live-123456",
      events: localEvents,
    });

    render(ThreadMessage, { props: { turn, loadTrace } });

    const btn = screen.getByText(/show trace/i).closest("button")!;
    await fireEvent.click(btn);

    await vi.waitFor(() => {
      expect(screen.getByText("local thought")).toBeDefined();
    });

    expect(loadTrace).not.toHaveBeenCalled();
  });

  test("synthetic turn_id starting with 'sys-' does NOT call loadTrace on expand", async () => {
    const localEvents: TurnEvent[] = [{ type: "thinking", text: "sys thought" }];
    const loadTrace = vi.fn().mockResolvedValue([]);
    const turn = makeTurn({
      role: "assistant",
      has_trace: true,
      turn_id: "sys-789",
      events: localEvents,
    });

    render(ThreadMessage, { props: { turn, loadTrace } });

    const btn = screen.getByText(/show trace/i).closest("button")!;
    await fireEvent.click(btn);

    await vi.waitFor(() => {
      expect(screen.getByText("sys thought")).toBeDefined();
    });

    expect(loadTrace).not.toHaveBeenCalled();
  });

  test("real turn_id with has_trace:true calls loadTrace on expand", async () => {
    const fetched: TurnEvent[] = [{ type: "thinking", text: "fetched thought" }];
    const loadTrace = vi.fn().mockResolvedValue(fetched);
    const turn = makeTurn({
      role: "assistant",
      has_trace: true,
      turn_id: "backend-uuid-abc",
      events: [],
    });

    render(ThreadMessage, { props: { turn, loadTrace } });

    const btn = screen.getByText(/show trace/i).closest("button")!;
    await fireEvent.click(btn);

    await vi.waitFor(() => {
      expect(screen.getByText("fetched thought")).toBeDefined();
    });

    expect(loadTrace).toHaveBeenCalledOnce();
    expect(loadTrace).toHaveBeenCalledWith("backend-uuid-abc");
  });

  test("tool events render as ToolCard inside trace section (not in bubble)", async () => {
    const localEvents: TurnEvent[] = [
      { type: "tool_use", tool_use_id: "t1", tool_name: "read_file", tool_input: '{"path":"/x"}' },
      { type: "tool_result", tool_use_id: "t1", text: "contents", is_error: false },
    ];
    const turn = makeTurn({
      role: "assistant",
      has_trace: false,
      events: localEvents,
      text: "Here is the file",
    });

    const { container } = render(ThreadMessage, { props: { turn } });

    const bubble = container.querySelector(".rounded-2xl");
    expect(bubble?.innerHTML).not.toContain("read_file");

    const btn = screen.getByText(/show trace/i).closest("button")!;
    await fireEvent.click(btn);

    await vi.waitFor(() => {
      expect(screen.getByText("read_file")).toBeDefined();
    });
  });
});
