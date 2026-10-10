import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ConversationThread from "../ConversationThread.svelte";
import type { ConversationTurn, LiveTurn, TeamTaskItem, ThreadBlock, TypingState } from "../../types/agents.js";
import { TEAM_TASKS_KEY, type TeamTasksCtx } from "../../teamTasksContext.js";
import { liveTurnTasks, tasksByTurn } from "../../delegations.js";

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
  test("while history loads it shows a loading state, not the agent's empty card", () => {
    const agent = { id: "a1", handle: "captain", name: "Captain", description: "Lead agent" };
    const { rerender } = render(ConversationThread, { props: { turns: [], live: null, typing: { active: false }, loading: true, agent } });
    expect(screen.getByTestId("thread-loading").textContent).toContain("Loading messages");
    expect(screen.queryByText("Lead agent")).toBeNull();
    rerender({ turns: [], live: null, typing: { active: false }, loading: false, agent });
    expect(screen.queryByTestId("thread-loading")).toBeNull();
    expect(screen.getByText("Lead agent")).toBeDefined();
  });

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

  test("live trace blocks are collapsed behind a toggle by default", async () => {
    const live: LiveTurn = {
      text: "",
      blocks: [
        { kind: "thinking", text: "secret thinking" },
        { kind: "tool", toolUseId: "t1", toolName: "Search", toolInput: "{}" },
      ],
    };
    const { container } = render(ConversationThread, {
      props: { turns: [], live, typing: { active: true, substate: "thinking" } },
    });
    expect(container.innerHTML).toContain("show trace");
    expect(container.innerHTML).toContain("2 steps");
    expect(screen.queryByText("secret thinking")).toBeNull();
    await fireEvent.click(screen.getByText("show trace"));
    expect(screen.getByText("secret thinking")).toBeDefined();
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

  test("typing indicator prefers a known tool name over substate", () => {
    const typing: TypingState = { active: true, substate: "working", toolName: "write_file" };
    const { container } = render(ConversationThread, {
      props: { turns: [], live: null, typing },
    });
    expect(container.innerHTML).toContain("writing file…");
    expect(container.innerHTML).not.toContain("running working…");
  });

  test("typing indicator falls back to a generic label for an unknown tool name", () => {
    const typing: TypingState = { active: true, toolName: "wick_search" };
    const { container } = render(ConversationThread, {
      props: { turns: [], live: null, typing },
    });
    expect(container.innerHTML).toContain("searching…");
  });

  test("typing indicator never renders the contradictory 'running idle…' text", () => {
    const typing: TypingState = { active: true, substate: "idle" };
    const { container } = render(ConversationThread, {
      props: { turns: [], live: null, typing },
    });
    expect(container.innerHTML).not.toContain("running idle…");
    expect(container.innerHTML).toContain("thinking…");
  });

  test("typing indicator never renders the raw backend substate 'running_tool'", () => {
    const typing: TypingState = { active: true, substate: "running_tool" };
    const { container } = render(ConversationThread, {
      props: { turns: [], live: null, typing },
    });
    expect(container.innerHTML).not.toContain("running running_tool…");
    expect(container.innerHTML).toContain("running a tool…");
  });

  test("renders empty state when no turns and not live/typing", () => {
    render(ConversationThread, { props: { turns: [], live: null, typing: { active: false } } });
    expect(screen.getByText(/no messages yet/i)).toBeDefined();
    expect(screen.getByText(/send a message to start/i)).toBeDefined();
  });

  test("does NOT render empty state when there are turns", () => {
    const turns = [{ turn_id: "t1", role: "user", agent: "", provider: "", text: "hi", timestamp: 0, truncated: false, interrupted: false, has_trace: false, events: [], attachments: [] }];
    const { container } = render(ConversationThread, { props: { turns, live: null, typing: { active: false } } });
    expect(container.innerHTML).not.toContain("No messages yet");
  });

  test("clicking a data-chat-path link calls onOpenPath with the path and prevents default navigation", async () => {
    const onOpenPath = vi.fn();
    const turn = makeTurn("t-path", "assistant", "See [x.ts](src/x.ts) for details");
    const { container } = render(ConversationThread, {
      props: { turns: [turn], live: null, typing: { active: false }, onOpenPath },
    });
    const link = container.querySelector<HTMLAnchorElement>("a[data-chat-path]");
    expect(link).not.toBeNull();
    expect(link!.getAttribute("data-chat-path")).toBe("src/x.ts");
    const ev = new MouseEvent("click", { bubbles: true, cancelable: true });
    link!.dispatchEvent(ev);
    expect(onOpenPath).toHaveBeenCalledOnce();
    expect(onOpenPath).toHaveBeenCalledWith("src/x.ts");
    expect(ev.defaultPrevented).toBe(true);
  });

  test("data-chat-path click is a no-op when onOpenPath is not provided", async () => {
    const turn = makeTurn("t-path-2", "assistant", "See [y.ts](src/y.ts) for details");
    const { container } = render(ConversationThread, {
      props: { turns: [turn], live: null, typing: { active: false } },
    });
    const link = container.querySelector<HTMLAnchorElement>("a[data-chat-path]");
    expect(link).not.toBeNull();
    const ev = new MouseEvent("click", { bubbles: true, cancelable: true });
    expect(() => link!.dispatchEvent(ev)).not.toThrow();
    expect(ev.defaultPrevented).toBe(true);
  });

  test("inserts a non-sticky day separator per day group and renders a floating day pill", () => {
    const now = new Date();
    const earlier = new Date(now.getTime() - 3 * 86400000);
    const older = { ...makeTurn("d-1", "user", "old day msg"), ts: earlier.toISOString() };
    const today = { ...makeTurn("d-2", "assistant", "today msg"), ts: now.toISOString() };
    const { container } = render(ConversationThread, {
      props: { turns: [older, today], live: null, typing: { active: false } },
    });
    const seps = container.querySelectorAll("[data-day-sep]");
    expect(seps.length).toBe(2);
    seps.forEach((s) => expect(s.className).not.toContain("sticky"));
    expect(container.querySelector("[data-day-label=\"Today\"]")).not.toBeNull();
    expect(container.querySelector("[data-floating-day]")).not.toBeNull();
  });

  test("renders all turns when multiple turns share empty turn_id without crashing", () => {
    const emptyA = makeTurn("", "user", "First empty-id turn");
    const emptyB = makeTurn("", "assistant", "Second empty-id turn");
    const dupId = makeTurn("t-dup", "user", "Third dup-id turn A");
    const dupIdB = makeTurn("t-dup", "assistant", "Fourth dup-id turn B");

    render(ConversationThread, {
      props: {
        turns: [emptyA, emptyB, dupId, dupIdB],
        live: null,
        typing: { active: false },
      },
    });

    expect(screen.getByText("First empty-id turn")).toBeDefined();
    expect(screen.getByText("Second empty-id turn")).toBeDefined();
    expect(screen.getByText("Third dup-id turn A")).toBeDefined();
    expect(screen.getByText("Fourth dup-id turn B")).toBeDefined();
  });
});

describe("ConversationThread - compaction in flight", () => {
  const typing: TypingState = { active: true, substate: "thinking" };

  test("says what is actually happening instead of 'thinking'", () => {
    render(ConversationThread, {
      props: { turns: [TURN_A], live: null, typing, compacting: true },
    });
    expect(screen.getByText(/compacting the conversation/i)).toBeDefined();
    expect(screen.queryByText("thinking…")).toBeNull();
  });

  test("an ordinary turn still reads as thinking", () => {
    render(ConversationThread, {
      props: { turns: [TURN_A], live: null, typing },
    });
    expect(screen.getByText("thinking…")).toBeDefined();
  });

  test("the compaction wait is visually distinct, not the usual grey bubble", () => {
    const { container } = render(ConversationThread, {
      props: { turns: [TURN_A], live: null, typing, compacting: true },
    });
    expect(container.innerHTML).toContain("amber");
  });
});

describe("ConversationThread working indicator", () => {
  test("stays mounted through a brief drop and hides only after the turn ends", async () => {
    vi.useFakeTimers();
    try {
      const props = (typing: TypingState) => ({ turns: [], live: null, typing, loading: false });
      const { rerender } = render(ConversationThread, { props: props({ active: true, substate: "thinking" }) });
      const first = screen.getByTestId("typing-label");
      expect(first.textContent).toBe("thinking…");
      // A drop between two events, then the turn goes on.
      await rerender(props({ active: false }));
      await vi.advanceTimersByTimeAsync(200);
      expect(screen.getByTestId("typing-label")).toBe(first);
      await rerender(props({ active: true, toolName: "shell" }));
      expect(screen.getByTestId("typing-label")).toBe(first);
      expect(first.textContent).toBe("running command…");
      // The real end: gone once the hide delay passed.
      await rerender(props({ active: false }));
      await vi.advanceTimersByTimeAsync(500);
      expect(screen.queryByTestId("typing-label")).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("ConversationThread remote progress label", () => {
  test("replaces thinking… while set; a tool label still wins", async () => {
    const props = (typing: TypingState, progressLabel?: string) => ({ turns: [], live: null, typing, loading: false, progressLabel });
    const { rerender } = render(ConversationThread, { props: props({ active: true, substate: "thinking" }, "lagi pakai code read…") });
    expect(screen.getByTestId("typing-label").textContent).toBe("lagi pakai code read…");
    await rerender(props({ active: true, toolName: "shell" }, "lagi pakai code read…"));
    expect(screen.getByTestId("typing-label").textContent).toBe("running command…");
    await rerender(props({ active: true, substate: "thinking" }));
    expect(screen.getByTestId("typing-label").textContent).toBe("thinking…");
  });
});

describe("ConversationThread working indicator placement", () => {
  test("sits above the turn's trace and stays while the live turn runs", async () => {
    vi.useFakeTimers();
    try {
      const live = { text: "", blocks: [{ kind: "tool", tool_use_id: "u1", name: "shell", input: "{}", status: "running" }] } as unknown as LiveTurn;
      const props = (typing: TypingState) => ({ turns: [], live, typing, loading: false });
      const { rerender, container } = render(ConversationThread, { props: props({ active: true, substate: "thinking" }) });
      const label = screen.getByTestId("typing-label");
      const toggle = container.querySelector("[data-live-trace-toggle]")!;
      expect(label.compareDocumentPosition(toggle) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      // Typing drops with no text yet: the live turn is still running.
      await rerender(props({ active: false }));
      await vi.advanceTimersByTimeAsync(2000);
      expect(screen.getByTestId("typing-label")).toBe(label);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("ConversationThread live delegation block", () => {
  const tasks: TeamTaskItem[] = [
    { task_id: "k1", context_id: "c", to_agent_id: "a1", to_handle: "alpha", to_name: "Alpha", title: "Check logs", state: "working", turns: 1, max_turns: 8, started_at: "2026-10-10T10:02:00Z", updated_at: "2026-10-10T10:02:00Z" },
    { task_id: "k2", context_id: "c", to_agent_id: "a2", to_handle: "beta", to_name: "Beta", title: "Check deploys", state: "working", turns: 1, max_turns: 8, started_at: "2026-10-10T10:02:00Z", updated_at: "2026-10-10T10:02:00Z" },
  ];
  const teamBlock = (id: string, taskId: string): ThreadBlock => ({ kind: "tool", toolUseId: id, toolName: "mcp__wick__team_message", toolInput: '{"to":"@x"}', result: `{"task_id":"${taskId}","state":"working"}` });
  const live: LiveTurn = {
    text: "",
    blocks: [teamBlock("u1", "k1"), teamBlock("u2", "k2"), { kind: "tool", toolUseId: "u3", toolName: "Search", toolInput: "{}", result: "ok" }],
  };
  const userTurn = { ...makeTurn("t-u", "user", "Split it across the team"), ts: "2026-10-10T10:01:00Z" };
  /* DetailView's wiring: the running turn's tasks stay off the committed
     turns (tasksByTurn) until it commits. */
  let state: { turns: ConversationTurn[]; live: LiveTurn | null } = { turns: [], live: null };
  const ctx: TeamTasksCtx = {
    forTurn: (id) => {
      const liveIds = new Set(liveTurnTasks(state.live?.blocks, tasks).map((t) => t.task_id));
      return tasksByTurn(state.turns, tasks.filter((t) => !liveIds.has(t.task_id))).get(id) ?? [];
    },
    tasks: () => tasks,
    agents: () => ({}),
    captainName: () => "Captain",
  };
  const context = () => new Map([[TEAM_TASKS_KEY, ctx]]);

  test("the running turn shows its block and drops the team_message cards from its trace", async () => {
    state = { turns: [userTurn], live };
    const { container } = render(ConversationThread, { props: { ...state, typing: { active: true } }, context: context() });
    const block = screen.getByTestId("delegation-block");
    expect(block.dataset.taskIds).toBe("k1 k2");
    expect(block.textContent).toContain("Delegated to 2 teammates");
    await fireEvent.click(screen.getByText("show trace"));
    // Only the Search card is left in the trace.
    expect(container.querySelectorAll("[data-tool-description]").length).toBe(1);
  });

  test("on commit the block moves to the committed turn: exactly one, never none", async () => {
    state = { turns: [userTurn], live };
    const { container, rerender } = render(ConversationThread, { props: { ...state, typing: { active: true } }, context: context() });
    expect(container.querySelectorAll("[data-task-ids]").length).toBe(1);
    // The thread store's finalize: the live turn becomes a live-* turn
    // (each result on its tool_use) in the same update that clears live.
    const committed: ConversationTurn = {
      ...makeTurn("live-1", "assistant", "Waiting on the team."),
      ts: "2026-10-10T10:03:00Z",
      has_trace: true,
      events: live.blocks.map((b) => (b.kind === "tool" ? { type: "tool_use", tool_name: b.toolName, tool_input: b.toolInput, tool_use_id: b.toolUseId, text: b.result } : { type: b.kind })),
    };
    state = { turns: [userTurn, committed], live: null };
    await rerender({ ...state, typing: { active: false } });
    const blocks = container.querySelectorAll("[data-task-ids]");
    expect(blocks.length).toBe(1);
    expect((blocks[0] as HTMLElement).dataset.taskIds).toBe("k1 k2");
    expect(container.querySelector('[data-turn-id="live-1"] [data-task-ids]')).not.toBeNull();
  });

  test("a live turn that only answers an earlier task shows no block and keeps its card", async () => {
    const answer: LiveTurn = { text: "", blocks: [{ kind: "tool", toolUseId: "u9", toolName: "mcp__wick__team_message", toolInput: '{"task_id":"k1","message":"both"}', result: '{"task_id":"k1","state":"working"}' }] };
    state = { turns: [userTurn], live: answer };
    const { container } = render(ConversationThread, { props: { ...state, typing: { active: true } }, context: context() });
    expect(container.querySelector("[data-task-ids]")).toBeNull();
    expect(container.innerHTML).toContain("1 step");
    await fireEvent.click(screen.getByText("show trace"));
    expect(container.querySelectorAll("[data-tool-description]").length).toBe(1);
  });

  test("a call whose task is not in the list yet keeps its card", async () => {
    const pending: LiveTurn = { text: "", blocks: [teamBlock("u7", "k9")] };
    state = { turns: [userTurn], live: pending };
    const { container } = render(ConversationThread, { props: { ...state, typing: { active: true } }, context: context() });
    expect(container.querySelector("[data-task-ids]")).toBeNull();
    await fireEvent.click(screen.getByText("show trace"));
    expect(container.querySelectorAll("[data-tool-description]").length).toBe(1);
  });

  test("the step count leaves out the cards the block stands for; none left, no toggle", () => {
    state = { turns: [userTurn], live };
    const { container, unmount } = render(ConversationThread, { props: { ...state, typing: { active: true } }, context: context() });
    expect(container.querySelector("[data-live-trace-toggle]")!.textContent).toContain("· 1 step");
    unmount();
    const onlyTeam: LiveTurn = { text: "", blocks: [teamBlock("u1", "k1"), teamBlock("u2", "k2")] };
    state = { turns: [userTurn], live: onlyTeam };
    const r = render(ConversationThread, { props: { ...state, typing: { active: true } }, context: context() });
    expect(r.getByTestId("delegation-block")).toBeDefined();
    expect(r.container.querySelector("[data-live-trace-toggle]")).toBeNull();
  });

  test("without the Team context the live turn renders as before", () => {
    render(ConversationThread, { props: { turns: [], live, typing: { active: true } } });
    expect(screen.queryByTestId("delegation-block")).toBeNull();
    expect(screen.getByText("show trace")).toBeDefined();
  });
});
