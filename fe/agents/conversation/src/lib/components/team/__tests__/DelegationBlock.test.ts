import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import DelegationBlock from "../DelegationBlock.svelte";
import TaskTray from "../TaskTray.svelte";
import TasksPanel from "../TasksPanel.svelte";
import ThreadMessage from "../../ThreadMessage.svelte";
import { TEAM_TASKS_KEY, type TeamTasksCtx } from "../../../teamTasksContext.js";
import type { ConversationTurn, TeamTaskItem } from "../../../types/agents.js";

function task(o: Partial<TeamTaskItem>): TeamTaskItem {
  return {
    task_id: "t1", context_id: "c1", to_agent_id: "a1", to_handle: "anton", to_name: "Anton",
    title: "check the logs", state: "working", turns: 1, max_turns: 8,
    started_at: "2026-10-10T10:00:00Z", updated_at: "2026-10-10T10:00:00Z", age: "2m",
    ...o,
  };
}

describe("DelegationBlock", () => {
  test("groups several team_message results into one block with a summary", () => {
    render(DelegationBlock, {
      props: {
        tasks: [
          task({ task_id: "a" }),
          task({ task_id: "b", to_handle: "vera", to_name: "Vera" }),
          task({ task_id: "c", to_handle: "rio", to_name: "Rio", state: "input_required", needs_you: true, reply: "Which env? (prod / staging)" }),
          task({ task_id: "d", to_handle: "lia", to_name: "Lia", state: "completed", summary: "done" }),
        ],
      },
    });
    expect(screen.getAllByTestId("delegation-block")).toHaveLength(1);
    expect(screen.getByText("Delegated to 4 teammates")).toBeDefined();
    expect(screen.getByTestId("delegation-summary").textContent).toBe("2 working · 1 needs input · 1 replied");
    expect(screen.getAllByTestId("delegation-chip")).toHaveLength(4);
  });

  test("needs you opens the answer card with quick replies; the answer goes to the task", async () => {
    const onAnswer = vi.fn().mockResolvedValue(undefined);
    render(DelegationBlock, {
      props: { tasks: [task({ state: "input_required", needs_you: true, reply: "Which env? (prod / staging)" })], captainName: "Cap", onAnswer },
    });
    expect(screen.getByTestId("needs-you-card")).toBeDefined();
    expect(screen.getByText("Answer goes to @anton · Cap sees it too")).toBeDefined();
    await fireEvent.click(screen.getByText("prod"));
    expect(onAnswer).toHaveBeenCalledWith("t1", "prod");
  });

  test("captain answering is a grey chip only, with Answer instead", async () => {
    render(DelegationBlock, {
      props: { tasks: [task({ state: "input_required", needs_you: false, reply: "Which env?" })], captainName: "Cap", onAnswer: vi.fn() },
    });
    expect(screen.queryByTestId("needs-you-card")).toBeNull();
    expect(screen.getByText("Cap is answering…")).toBeDefined();
    expect(screen.getByTestId("delegation-chip").dataset.status).toBe("answering");
    await fireEvent.click(screen.getByTestId("answer-instead"));
    expect(screen.getByTestId("needs-you-card")).toBeDefined();
  });

  test("a second answer refused by the server shows its message", async () => {
    const onAnswer = vi.fn().mockRejectedValue(new Error("that question was already answered — the first answer won"));
    render(DelegationBlock, { props: { tasks: [task({ state: "input_required", needs_you: true, reply: "Which env? (prod / staging)" })], onAnswer } });
    await fireEvent.click(screen.getByText("prod"));
    await vi.waitFor(() => expect(screen.getByTestId("answer-error").textContent).toContain("already answered"));
  });

  test("auto-collapses once every task ended, and unfolds on click", async () => {
    render(DelegationBlock, {
      props: { tasks: [task({ task_id: "a", state: "completed", turns: 2 }), task({ task_id: "b", to_handle: "vera", state: "completed", turns: 1 })] },
    });
    expect(screen.queryByTestId("delegation-block")).toBeNull();
    expect(screen.getByTestId("delegation-folded").textContent).toContain("Delegated to 2 teammates · all replied · 3 messages");
    await fireEvent.click(screen.getByTestId("delegation-folded"));
    expect(screen.getByTestId("delegation-block")).toBeDefined();
  });

  test("a failure is never folded behind all replied", () => {
    render(DelegationBlock, { props: { tasks: [task({ task_id: "a", state: "completed" }), task({ task_id: "b", state: "failed" })] } });
    expect(screen.getByTestId("delegation-folded").textContent).toContain("1 replied · 1 failed");
  });

  test("a chip previews the last messages with Open chat", async () => {
    const onOpenChat = vi.fn();
    render(DelegationBlock, { props: { tasks: [task({ summary: "found 3 errors", reply: "found 3 errors" })], onOpenChat } });
    expect(screen.queryByTestId("delegation-preview")).toBeNull();
    await fireEvent.click(screen.getByTestId("delegation-chip"));
    expect(screen.getByTestId("delegation-preview").textContent).toContain("found 3 errors");
    await fireEvent.click(screen.getByText("Open chat ↗"));
    expect(onOpenChat).toHaveBeenCalled();
  });
});

describe("DelegationBlock cancel", () => {
  test("Cancel in a preview asks first, then cancels the task", async () => {
    const onCancel = vi.fn().mockResolvedValue(undefined);
    render(DelegationBlock, { props: { tasks: [task({ task_id: "blk-1" })], onCancel } });
    await fireEvent.click(screen.getByTestId("delegation-chip"));
    await fireEvent.click(screen.getByTestId("delegation-cancel"));
    expect(onCancel).not.toHaveBeenCalled();
    expect(screen.getByTestId("cancel-confirm").textContent).toContain("Anton's current turn will be stopped");
    await fireEvent.click(screen.getByTestId("cancel-confirm-yes"));
    expect(onCancel).toHaveBeenCalledWith("blk-1");
  });

  test("an interrupted task's chip reads Interrupted by a restart, with no Cancel", async () => {
    render(DelegationBlock, { props: { tasks: [task({ task_id: "blk-2", state: "failed", interrupted: true })], onCancel: vi.fn() } });
    await fireEvent.click(screen.getByTestId("delegation-folded"));
    expect(screen.getByTestId("delegation-chip").textContent).toContain("Interrupted by a restart");
    await fireEvent.click(screen.getByTestId("delegation-chip"));
    expect(screen.queryByTestId("delegation-cancel")).toBeNull();
  });
});

describe("TaskTray", () => {
  test("is absent when no task is active", () => {
    render(TaskTray, { props: { tasks: [task({ state: "completed" })] } });
    expect(screen.queryByTestId("task-tray")).toBeNull();
  });

  test("shows the active tasks and Answer when one needs you; Cancel calls the task", async () => {
    const onCancel = vi.fn().mockResolvedValue(undefined);
    render(TaskTray, {
      props: { tasks: [task({ task_id: "a" }), task({ task_id: "b", state: "input_required", needs_you: true }), task({ task_id: "c", state: "completed" })], onAnswer: vi.fn(), onCancel },
    });
    expect(screen.getByTestId("task-tray-summary").textContent).toBe("1 working · 1 needs input");
    await fireEvent.click(screen.getByText("Answer"));
    expect(screen.getByTestId("task-tray-popover")).toBeDefined();
    await fireEvent.click(screen.getAllByText("Cancel")[0]);
    // Cancel asks first: the teammate's current turn will be stopped.
    expect(onCancel).not.toHaveBeenCalled();
    expect(screen.getByTestId("cancel-confirm").textContent).toContain("current turn will be stopped");
    await fireEvent.click(screen.getByTestId("cancel-confirm-yes"));
    expect(onCancel).toHaveBeenCalledWith("a");
  });

  test("Keep working closes the confirm without canceling", async () => {
    const onCancel = vi.fn().mockResolvedValue(undefined);
    render(TaskTray, { props: { tasks: [task({ task_id: "a" })], onCancel } });
    await fireEvent.click(screen.getByTestId("task-tray-summary"));
    await fireEvent.click(screen.getByText("Cancel"));
    await fireEvent.click(screen.getByTestId("cancel-confirm-keep"));
    expect(screen.queryByTestId("cancel-confirm")).toBeNull();
    expect(onCancel).not.toHaveBeenCalled();
  });

  test("canceling one task keeps the answer typed for another", async () => {
    const onCancel = vi.fn().mockResolvedValue(undefined);
    render(TaskTray, {
      props: { tasks: [task({ task_id: "tray-q", state: "input_required", needs_you: true }), task({ task_id: "tray-w", to_handle: "vera", to_name: "Vera" })], onAnswer: vi.fn(), onCancel },
    });
    await fireEvent.click(screen.getAllByText("Answer")[0]);
    const input = screen.getByLabelText("Answer @anton") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "use staging" } });
    await fireEvent.click(screen.getAllByText("Cancel")[1]);
    await fireEvent.click(screen.getByTestId("cancel-confirm-yes"));
    expect(onCancel).toHaveBeenCalledWith("tray-w");
    expect((screen.getByLabelText("Answer @anton") as HTMLInputElement).value).toBe("use staging");
  });
});

describe("TasksPanel", () => {
  test("groups Needs you / Working / Done / Failed·Canceled with a raw payload", () => {
    render(TasksPanel, {
      props: {
        tasks: [
          task({ task_id: "a", state: "input_required", needs_you: true }),
          task({ task_id: "b" }),
          task({ task_id: "c", state: "completed" }),
          task({ task_id: "d", state: "canceled" }),
        ],
      },
    });
    for (const g of ["needs_you", "working", "done", "failed"]) expect(screen.getByTestId("tasks-group-" + g)).toBeDefined();
    expect(screen.getAllByText("Raw payload")).toHaveLength(4);
  });

  test("Cancel asks before it cancels", async () => {
    const onCancel = vi.fn().mockResolvedValue(undefined);
    render(TasksPanel, { props: { tasks: [task({ task_id: "p1" })], onCancel } });
    await fireEvent.click(screen.getByText("Cancel"));
    expect(onCancel).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByTestId("cancel-confirm-yes"));
    expect(onCancel).toHaveBeenCalledWith("p1");
  });

  test("a task a restart interrupted says so", () => {
    render(TasksPanel, { props: { tasks: [task({ task_id: "p2", state: "failed", interrupted: true, summary: "interrupted by a restart: …" })] } });
    expect(screen.getByText(/interrupted by a restart · 2m/)).toBeDefined();
  });
});

describe("Answer drafts", () => {
  test("survive a remount, keyed by task, and clear only after a sent answer", async () => {
    const q = task({ task_id: "draft-1", state: "input_required", needs_you: true, reply: "Which env?" });
    const failing = vi.fn().mockRejectedValue(new Error("network down"));
    const first = render(DelegationBlock, { props: { tasks: [q], onAnswer: failing } });
    await fireEvent.input(screen.getByLabelText("Answer @anton"), { target: { value: "prod, after 6pm" } });
    first.unmount();

    // A refetch remounts the block: the draft is still there.
    const second = render(DelegationBlock, { props: { tasks: [{ ...q, age: "3m" }], onAnswer: failing } });
    expect((screen.getByLabelText("Answer @anton") as HTMLInputElement).value).toBe("prod, after 6pm");
    // A failed send keeps it.
    await fireEvent.click(screen.getByText("Send"));
    expect(await screen.findByTestId("answer-error")).toBeDefined();
    second.unmount();

    // Another task's block starts empty; the tray shows the same draft.
    const other = render(DelegationBlock, { props: { tasks: [task({ task_id: "draft-2", to_handle: "vera", state: "input_required", needs_you: true })], onAnswer: failing } });
    expect((screen.getByLabelText("Answer @vera") as HTMLInputElement).value).toBe("");
    other.unmount();
    const sent = vi.fn().mockResolvedValue(undefined);
    const tray = render(TaskTray, { props: { tasks: [q], onAnswer: sent } });
    await fireEvent.click(screen.getAllByText("Answer")[0]);
    expect((screen.getByLabelText("Answer @anton") as HTMLInputElement).value).toBe("prod, after 6pm");
    await fireEvent.click(screen.getByText("Send"));
    expect(sent).toHaveBeenCalledWith("draft-1", "prod, after 6pm");
    tray.unmount();

    // Sent: gone everywhere.
    render(DelegationBlock, { props: { tasks: [q], onAnswer: sent } });
    expect((screen.getByLabelText("Answer @anton") as HTMLInputElement).value).toBe("");
  });
});

describe("ThreadMessage delegation block", () => {
  const turn: ConversationTurn = {
    turn_id: "turn-1", role: "assistant", agent: "main", provider: "p", text: "Sent them off.", timestamp: 0,
    truncated: false, interrupted: false, has_trace: true, attachments: [],
    events: [
      { type: "tool_use", tool_use_id: "u1", tool_name: "mcp__wick__team_message", tool_input: '{"to":"@anton"}' },
      { type: "tool_result", tool_use_id: "u1", text: '{"task_id":"t1","state":"working"}' },
    ],
  } as ConversationTurn;

  test("renders the block from context and drops the raw team_message card", async () => {
    const ctx: TeamTasksCtx = {
      forTurn: (id) => (id === "turn-1" ? [task({})] : []),
      agents: () => ({}),
      captainName: () => "Cap",
      answer: vi.fn(),
      cancel: vi.fn(),
    };
    render(ThreadMessage, { props: { turn }, context: new Map([[TEAM_TASKS_KEY, ctx]]) });
    expect(screen.getByTestId("delegation-block")).toBeDefined();
    await fireEvent.click(screen.getByText(/show trace/));
    expect(screen.queryByText(/team_message/)).toBeNull();
  });

  test("P45: the person's own task — 'You asked', straight to Needs you, no answering stage", async () => {
    const onAnswer = vi.fn().mockResolvedValue(undefined);
    render(DelegationBlock, {
      props: { tasks: [task({ origin: "user", state: "input_required", needs_you: false, reply: "Which env? (prod / staging)" })], captainName: "Cap", onAnswer },
    });
    expect(screen.getByText("You asked 1 teammate")).toBeDefined();
    expect(screen.getByTestId("user-task-marker").textContent).toBe("You asked");
    const chip = screen.getByTestId("delegation-chip");
    expect(chip.getAttribute("data-status")).toBe("needs_you");
    expect(chip.getAttribute("data-origin")).toBe("user");
    expect(chip.textContent).toContain("Needs you");
    expect(chip.textContent).not.toContain("is answering");
    expect(screen.queryByTestId("answer-instead")).toBeNull();
    expect(screen.getByTestId("needs-you-card")).toBeDefined();
    expect(screen.getByText("Answer goes to @anton · you asked, so only you answer")).toBeDefined();
    await fireEvent.click(chip);
    expect(screen.getByText("You → @anton")).toBeDefined();
  });

  test("P45: the teammate's final reply shows on the person's task", async () => {
    render(DelegationBlock, {
      props: { tasks: [task({ origin: "user", state: "completed", reply: "deployed to prod", summary: "deployed to prod" })], captainName: "Cap" },
    });
    await fireEvent.click(screen.getByTestId("delegation-folded"));
    await fireEvent.click(screen.getByTestId("delegation-chip"));
    expect(screen.getByText("deployed to prod")).toBeDefined();
    expect(screen.getByText("You → @anton")).toBeDefined();
  });

  test("P45: tray and Tasks tab name the person's task 'You → @B'", async () => {
    const mine = task({ task_id: "m", origin: "user", state: "input_required", needs_you: false });
    const { unmount } = render(TaskTray, { props: { tasks: [mine, task({ task_id: "a" })], onAnswer: vi.fn() } });
    await fireEvent.click(screen.getByTestId("task-tray-summary"));
    const names = screen.getAllByTestId("task-tray-name").map((e) => e.textContent);
    expect(names).toContain("You → @anton");
    expect(names).toContain("Anton");
    unmount();
    render(TasksPanel, { props: { tasks: [mine], senderName: "Cap" } });
    expect(screen.getByTestId("tasks-panel-name").textContent).toBe("You → @anton");
    expect(screen.queryByText(/Cap is answering/)).toBeNull();
  });
});

/* P39: a teammate's avatar in the chip and the tray moves while its task
   works and pulses while a question waits for the person. */
describe("task avatar activity", () => {
  const tasks = [
    task({ task_id: "w" }),
    task({ task_id: "q", to_handle: "rio", to_name: "Rio", state: "input_required", needs_you: true, reply: "Which env?" }),
    task({ task_id: "a", to_handle: "vera", to_name: "Vera", state: "input_required", reply: "Which env?" }),
    task({ task_id: "d", to_handle: "lia", to_name: "Lia", state: "completed", summary: "done" }),
  ];
  const activities = (root: ParentNode) => [...root.querySelectorAll<HTMLElement>("[data-testid=avatar-activity]")].map((e) => e.dataset.activity);

  test("delegation chips", () => {
    render(DelegationBlock, { props: { tasks } });
    expect(screen.getAllByTestId("delegation-chip").map((c) => activities(c)[0])).toEqual(["tool", "alert", "idle", "idle"]);
  });

  test("task tray, stacked avatars", () => {
    const { container } = render(TaskTray, { props: { tasks } });
    expect(activities(container)).toEqual(["tool", "alert", "idle"]);
  });

  test("task tray popover rows", async () => {
    render(TaskTray, { props: { tasks } });
    await fireEvent.click(screen.getByTestId("task-tray-summary"));
    const rows = screen.getAllByTestId("task-tray-name").map((n) => activities(n.parentElement!)[0]);
    expect(rows).toEqual(["tool", "alert", "idle"]);
  });

  test("Tasks rail: a working teammate orbits, one waiting on you pulses", () => {
    const { container } = render(TasksPanel, { props: { tasks } });
    const rows = screen.getAllByTestId("tasks-panel-name").map((n) => n.parentElement!);
    const by = (name: string) => rows.find((r) => r.textContent!.includes(name))!;
    expect(activities(by("Anton"))).toEqual(["tool"]);
    expect(by("Anton").querySelector("[data-testid=avatar-orbit]")).not.toBeNull();
    expect(activities(by("Rio"))).toEqual(["alert"]);
    expect(activities(by("Vera"))).toEqual(["idle"]);
    expect(activities(by("Lia"))).toEqual(["idle"]);
    expect(container.querySelectorAll("[data-testid=avatar-orbit]").length).toBe(1);
  });
});
