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

  test("the running turn shows its block, and its trace keeps the team_message cards with a live row each (T16 S6)", async () => {
    state = { turns: [userTurn], live };
    const { container } = render(ConversationThread, { props: { ...state, typing: { active: true } }, context: context() });
    const block = screen.getByTestId("delegation-block");
    expect(block.dataset.taskIds).toBe("k1 k2");
    expect(block.textContent).toContain("Delegated to 2 teammates");
    await fireEvent.click(screen.getByText("show trace"));
    expect(container.querySelectorAll("[data-tool-description]").length).toBe(3);
    const rows = screen.getAllByTestId("team-trace-row");
    expect(rows.map((r) => r.textContent?.includes("@alpha") || r.textContent?.includes("@beta"))).toEqual([true, true]);
    expect(rows.every((r) => r.dataset.status === "working")).toBe(true);
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

  test("T16 S6: the step count counts the team_message cards the trace now keeps; a turn of only those still has its toggle", () => {
    state = { turns: [userTurn], live };
    const { container, unmount } = render(ConversationThread, { props: { ...state, typing: { active: true } }, context: context() });
    expect(container.querySelector("[data-live-trace-toggle]")!.textContent).toContain("· 3 steps");
    unmount();
    const onlyTeam: LiveTurn = { text: "", blocks: [teamBlock("u1", "k1"), teamBlock("u2", "k2")] };
    state = { turns: [userTurn], live: onlyTeam };
    const r = render(ConversationThread, { props: { ...state, typing: { active: true } }, context: context() });
    expect(r.getByTestId("delegation-block")).toBeDefined();
    expect(r.container.querySelector("[data-live-trace-toggle]")!.textContent).toContain("· 2 steps");
  });

  test("the live turn opens with the asst-meta status line: who, when, how many tasks", () => {
    state = { turns: [userTurn], live };
    render(ConversationThread, { props: { ...state, typing: { active: true }, agent: { name: "Captain", description: "", caption: "" } }, context: context() });
    const meta = screen.getByTestId("live-asst-meta");
    expect(meta.textContent!.trim()).toMatch(/^Captain · \d\d:\d\d · split the question into 2 tasks — summary comes when they're back$/);
    // It stands right above the block.
    expect(meta.nextElementSibling?.hasAttribute("data-task-ids")).toBe(true);
  });

  test("one task names the teammate; the line goes once every task settled", async () => {
    const one: LiveTurn = { text: "", blocks: [teamBlock("u1", "k1")] };
    state = { turns: [userTurn], live: one };
    const { rerender } = render(ConversationThread, { props: { ...state, typing: { active: true }, agent: { name: "Captain", description: "", caption: "" } }, context: context() });
    expect(screen.getByTestId("live-asst-meta").textContent).toContain("· asked Alpha — summary comes when they're back");
    tasks[0] = { ...tasks[0], state: "completed", ended_at: "2026-10-10T10:05:00Z" };
    try {
      await rerender({ ...state, live: { ...one }, typing: { active: true } });
      expect(screen.queryByTestId("live-asst-meta")).toBeNull();
      expect(document.querySelector("[data-task-ids]")).not.toBeNull();
    } finally {
      tasks[0] = { ...tasks[0], state: "working", ended_at: undefined };
    }
  });

  test("a committed turn never shows the live status line", () => {
    render(ConversationThread, { props: { turns: [userTurn, makeTurn("t-x", "assistant", "done")], live: null, typing: { active: false } }, context: context() });
    expect(screen.queryByTestId("live-asst-meta")).toBeNull();
  });

  test("names carrying HTML render as literal text, no element made", () => {
    const evil = "<img src=x onerror=alert(1)>";
    const one: LiveTurn = { text: "", blocks: [teamBlock("u1", "k1")] };
    tasks[0] = { ...tasks[0], to_name: evil };
    try {
      state = { turns: [userTurn], live: one };
      render(ConversationThread, { props: { ...state, typing: { active: true }, agent: { name: evil, description: "", caption: "" } }, context: context() });
      const meta = screen.getByTestId("live-asst-meta");
      expect(meta.textContent).toContain(evil + " · ");
      expect(meta.textContent).toContain("asked " + evil + " — ");
      expect(meta.querySelector("img")).toBeNull();
      expect(document.querySelector('img[src="x"]')).toBeNull();
    } finally {
      tasks[0] = { ...tasks[0], to_name: "Alpha" };
    }
  });

  test("no agent name reads Assistant; no start time leaves the clock out", () => {
    const k1 = tasks[0];
    tasks[0] = { ...k1, started_at: "" };
    try {
      state = { turns: [userTurn], live: { text: "", blocks: [teamBlock("u1", "k1")] } };
      render(ConversationThread, { props: { ...state, typing: { active: true } }, context: context() });
      const meta = screen.getByTestId("live-asst-meta");
      expect(meta.textContent!.trim()).toBe("Assistant · asked Alpha — summary comes when they're back");
      expect(meta.querySelector('[aria-hidden="true"]')).not.toBeNull();
    } finally {
      tasks[0] = k1;
    }
  });

  test("P60 S2: the running turn's in-between text sits in its trace, counted as a step; a question shows", async () => {
    const text = "Asked both of them. Still checking.";
    state = { turns: [userTurn], live: { text, blocks: [teamBlock("u1", "k1"), teamBlock("u2", "k2")] } };
    const { container, rerender } = render(ConversationThread, { props: { ...state, typing: { active: true } }, context: context() });
    expect(screen.getByTestId("delegation-block")).toBeDefined();
    expect(screen.queryByText(text)).toBeNull();
    expect(container.querySelector("[data-live-trace-toggle]")!.textContent).toContain("3 steps");
    await fireEvent.click(screen.getByText("show trace"));
    expect(screen.getByText(text)).toBeDefined();
    await fireEvent.click(screen.getByText("hide trace"));
    state = { ...state, live: { ...state.live!, text: text + "\nShould I wait?" } };
    await rerender({ ...state, typing: { active: true } });
    expect(container.querySelector("[data-live-trace-toggle]")!.textContent).toContain("2 steps");
    expect(container.textContent).toContain("Should I wait?");
  });

  test("P60 S2: live → committed keeps the in-between text out of the body, no flicker", async () => {
    const text = "Asked both of them. Still checking.";
    const blocks = [teamBlock("u1", "k1"), teamBlock("u2", "k2")];
    state = { turns: [userTurn], live: { text, blocks } };
    const body = (c: HTMLElement) => [...c.querySelectorAll("[data-turn-id], .rounded-2xl")].some((el) => !el.closest("[data-trace-blocks]") && el.textContent?.includes(text));
    const { container, rerender } = render(ConversationThread, { props: { ...state, typing: { active: true } }, context: context() });
    expect(body(container)).toBe(false);
    const committed: ConversationTurn = {
      ...makeTurn("live-2", "assistant", text),
      ts: "2026-10-10T10:03:00Z",
      has_trace: true,
      events: blocks.map((b) => (b.kind === "tool" ? { type: "tool_use", tool_name: b.toolName, tool_input: b.toolInput, tool_use_id: b.toolUseId, text: b.result } : { type: b.kind })),
    };
    state = { turns: [userTurn, committed], live: null };
    await rerender({ ...state, typing: { active: false } });
    expect(screen.getByTestId("delegation-block")).toBeDefined();
    expect(container.textContent).not.toContain(text);
  });

  test("P60 S2: a turn that also did other work keeps its live text", () => {
    state = { turns: [userTurn], live: { ...live, text: "Looked it up and asked them." } };
    const { container } = render(ConversationThread, { props: { ...state, typing: { active: true } }, context: context() });
    expect(container.textContent).toContain("Looked it up and asked them.");
  });

  test("without the Team context the live turn renders as before", () => {
    render(ConversationThread, { props: { turns: [], live, typing: { active: true } } });
    expect(screen.queryByTestId("delegation-block")).toBeNull();
    expect(screen.getByText("show trace")).toBeDefined();
  });
});

describe("ConversationThread one UI per delegation (T16 S9)", () => {
  const t1: TeamTaskItem = { task_id: "k1", context_id: "c", to_agent_id: "a1", to_handle: "research", to_name: "Research", title: "Check PR", state: "completed", turns: 1, max_turns: 8, started_at: "2026-10-10T10:02:00Z", updated_at: "2026-10-10T10:04:00Z", reply: "PR is open" };
  const sender: ConversationTurn = {
    ...makeTurn("a1", "assistant", "Asked the team."),
    events: [
      { type: "tool_use", tool_use_id: "u1", tool_name: "mcp__wick__team_message", tool_input: '{"to":"@research"}' },
      { type: "tool_result", tool_use_id: "u1", text: '{"task_id":"k1","state":"working"}' },
    ],
  };
  const ctx: TeamTasksCtx = { forTurn: (id) => (id === "a1" ? [t1] : []), tasks: () => [t1], agents: () => ({ research: { name: "Research" } }), captainName: () => "Captain" };
  const wake = (text: string): ConversationTurn => ({ ...makeTurn("w" + text.length, "user", text), source: "subagent" });
  const handoff: ConversationTurn = { ...makeTurn("h1", "system", "handoff"), kind: "mention_handoff", extras: { from: "main", to: "research", state: "completed", task_id: "k1" } };

  test("P60 S2: a settled delegation's in-between text shows until a later answer exists", () => {
    const r = render(ConversationThread, {
      props: { turns: [makeTurn("u0", "user", "check it"), sender], live: null, typing: { active: false } },
      context: new Map([[TEAM_TASKS_KEY, ctx]]),
    });
    expect(r.container.textContent).toContain("Asked the team.");
    r.unmount();
    const { container } = render(ConversationThread, {
      props: { turns: [makeTurn("u0", "user", "check it"), sender, makeTurn("a2", "assistant", "One answer.")], live: null, typing: { active: false } },
      context: new Map([[TEAM_TASKS_KEY, ctx]]),
    });
    expect(container.textContent).not.toContain("Asked the team.");
    expect(container.textContent).toContain("One answer.");
  });

  test("P60 S2: a later answer arriving while mounted moves the text into the trace; a teammate reply or another in-between text does not", async () => {
    const turns = [makeTurn("u0", "user", "check it"), sender];
    const { container, rerender } = render(ConversationThread, { props: { turns, live: null, typing: { active: false } }, context: new Map([[TEAM_TASKS_KEY, ctx]]) });
    expect(container.textContent).toContain("Asked the team.");
    const mention: ConversationTurn = { ...makeTurn("m1", "assistant", "Sure, here you go."), speaker: { agent_id: "x", handle: "main", via: "mention" } };
    await rerender({ turns: [...turns, mention], live: null, typing: { active: false } });
    expect(container.textContent).toContain("Asked the team.");
    await rerender({ turns: [...turns, mention, makeTurn("a2", "assistant", "One answer.")], live: null, typing: { active: false } });
    expect(container.textContent).not.toContain("Asked the team.");
  });

  test("the batch prompt and the hand-off pill leave the thread; the block keeps the reply", () => {
    const batch = wake('[team results · group b1 · 1 of 1 replied]\nResearch — completed [task k1]:\n<reply from="@research" task="k1" state="completed">\nPR is open\n</reply>');
    const { container } = render(ConversationThread, {
      props: { turns: [makeTurn("u0", "user", "check it"), handoff, sender, batch, makeTurn("a2", "assistant", "One answer.")], live: null, typing: { active: false } },
      context: new Map([[TEAM_TASKS_KEY, ctx]]),
    });
    expect(container.querySelector('[data-turn-id="h1"]')).toBeNull();
    expect(container.querySelector(`[data-turn-id="${batch.turn_id}"]`)).toBeNull();
    expect(screen.queryByText(/team results/)).toBeNull();
    expect(container.querySelectorAll("[data-task-ids]").length).toBe(1);
  });

  test("a late reply's update prompt stays as one 'Updated with' line", () => {
    const upd = wake('[team results update · group b1 · Updated with @research\'s reply · 2 of 2 replied]\n<reply from="@research" task="k1" state="completed">\nx\n</reply>\n[task k1]');
    render(ConversationThread, {
      props: { turns: [sender, upd, makeTurn("a3", "assistant", "Updated answer.")], live: null, typing: { active: false } },
      context: new Map([[TEAM_TASKS_KEY, ctx]]),
    });
    expect(screen.getByTestId("team-update-label").textContent).toContain("Updated with Research's reply");
  });

  test("without a block the prompt still renders as before", () => {
    const batch = wake("[team results · group b9 · 1 of 1 replied]\nGone — completed [task zz]:\nhi");
    const { container } = render(ConversationThread, {
      props: { turns: [batch], live: null, typing: { active: false } },
      context: new Map([[TEAM_TASKS_KEY, ctx]]),
    });
    expect(container.querySelector(`[data-turn-id="${batch.turn_id}"]`)).not.toBeNull();
  });
});
