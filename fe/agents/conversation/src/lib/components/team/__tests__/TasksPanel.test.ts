import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/svelte";
import { tick } from "svelte";
import TasksPanel from "../TasksPanel.svelte";
import type { TeamTaskItem } from "../../../types/agents.js";

/* P48 s8: the Tasks rail tab per the combined mockup §4. Every date is
   local and the clock is fixed, so Done today / Earlier never depend on
   when the suite runs. */
const at = (h: number, m = 0, s = 0, day = 10) => new Date(2026, 9, day, h, m, s).toISOString();

function task(o: Partial<TeamTaskItem>): TeamTaskItem {
  return {
    task_id: "t1", context_id: "c1", to_agent_id: "a1", to_handle: "anton", to_name: "Anton",
    title: "check the logs", state: "working", turns: 1, max_turns: 8,
    started_at: at(11, 57, 50), updated_at: at(11, 57, 50),
    ...o,
  };
}

const rowOf = (name: string) =>
  screen.getAllByTestId("tasks-row").find((r) => within(r).getByTestId("tasks-panel-name").textContent === name)!;

describe("TasksPanel (combined §4)", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 9, 10, 12, 0, 0));
  });
  afterEach(() => vi.useRealTimers());

  const tasks = [
    task({ task_id: "q", to_handle: "rio", to_name: "Rio", state: "input_required", needs_you: true, updated_at: at(11, 57, 50), summary: "Diff both deploys? (both / only 20:55)" }),
    task({ task_id: "w", to_handle: "anton", to_name: "Anton", started_at: at(11, 57, 50) }),
    task({ task_id: "d", to_handle: "lia", to_name: "Lia", state: "completed", title: "were other clients hit?", updated_at: at(10, 8) }),
    task({ task_id: "e", to_handle: "vera", to_name: "Vera", state: "completed", title: "old check", updated_at: at(18, 0, 0, 9) }),
    task({ task_id: "f", to_handle: "kai", to_name: "Kai", state: "failed", title: "pull logs", summary: "No reply in 10 min" }),
    task({ task_id: "c", to_handle: "mo", to_name: "Mo", state: "canceled", title: "scan all", summary: "Canceled by you — too wide" }),
  ];

  test("header, group titles and the folded Earlier group", async () => {
    render(TasksPanel, { props: { tasks } });
    expect(screen.getByText("Tasks")).toBeDefined();
    expect(screen.getByTestId("tasks-panel-count").textContent).toBe("this chat · 6");
    const titles = [...document.querySelectorAll("section h3")].map((h) => h.textContent!.trim());
    expect(titles).toEqual(["Needs input · 1", "Working · 1", "Done today · 1", "Earlier · 1", "Failed · Canceled · 2"]);
    expect(document.querySelector("[data-testid=tasks-group-needs_you] h3")!.className).toContain("text-amber-700");
    // Earlier is under Done today and folded: nothing disappears, it is one click away.
    const toggle = screen.getByTestId("tasks-earlier-toggle");
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByText("old check")).toBeNull();
    await fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByText("old check")).toBeDefined();
    expect(within(rowOf("Vera")).getByTestId("tasks-row-time").textContent).toBe("Oct 9");
  });

  test("rows: tags, times, title or reason, the question in amber", () => {
    render(TasksPanel, { props: { tasks } });
    const q = rowOf("Rio");
    expect(q.className).toContain("border-amber-500/[.45]");
    expect(within(q).queryByTestId("tasks-row-tag")).toBeNull();
    expect(within(q).getByTestId("tasks-row-time").textContent).toBe("2m 10s");
    expect(within(q).getByTestId("tasks-row-question").textContent).toBe("Diff both deploys? (both / only 20:55)");
    expect(within(rowOf("Anton")).getByTestId("tasks-row-time").textContent).toBe("2m 10s");

    const d = rowOf("Lia");
    expect(within(d).getByTestId("tasks-row-tag").textContent).toBe("replied");
    expect(within(d).getByTestId("tasks-row-tag").className).toContain("text-green-600");
    expect(within(d).getByTestId("tasks-row-time").textContent).toBe("10:08");
    expect(within(d).getByText("were other clients hit?")).toBeDefined();

    const f = rowOf("Kai");
    expect(within(f).getByTestId("tasks-row-tag").className).toContain("text-red-600");
    expect(within(f).getByText("No reply in 10 min")).toBeDefined();
    expect(within(f).queryByText("pull logs")).toBeNull();
    const c = rowOf("Mo");
    expect(within(c).getByTestId("tasks-row-tag").textContent).toBe("canceled");
    expect(within(c).getByText("Canceled by you — too wide")).toBeDefined();
  });

  test("actions: Answer / quiet Cancel / Jump, working Cancel / Jump, none once ended", () => {
    const fn = () => vi.fn().mockResolvedValue(undefined);
    render(TasksPanel, { props: { tasks, onAnswer: fn(), onCancel: fn(), onJump: vi.fn() } });
    const btns = (name: string) => within(rowOf(name)).getAllByRole("button").map((b) => b.textContent!.trim());
    const q = rowOf("Rio");
    expect(within(q).getByText("Answer").closest("button")!.dataset.variant).toBe("warn");
    expect(within(q).getByText("Cancel").closest("button")!.dataset.variant).toBeUndefined();
    expect(within(q).getAllByText("Jump")[0].closest("button")!.dataset.variant).toBe("default");
    const w = rowOf("Anton");
    expect(within(w).getByText("Cancel").closest("button")!.dataset.variant).toBe("danger");
    expect(within(w).queryByText("Answer")).toBeNull();
    // Ended rows: no action buttons beyond the quiet links by the raw payload.
    for (const n of ["Lia", "Kai", "Mo"]) expect(btns(n).filter((t) => t === "Cancel" || t === "Answer")).toEqual([]);
    expect(within(rowOf("Mo")).queryAllByRole("button").some((b) => b.dataset.variant)).toBe(false);
  });

  test("Answer opens the shared AnswerBox and sends", async () => {
    const onAnswer = vi.fn().mockResolvedValue(undefined);
    render(TasksPanel, { props: { tasks, onAnswer } });
    await fireEvent.click(within(rowOf("Rio")).getByText("Answer"));
    const box = screen.getByTestId("answer-box");
    expect(within(box).getByTestId("answer-to").textContent).toContain("@rio");
    expect(within(box).getAllByTestId("quick-pill").map((p) => p.textContent)).toEqual(["both", "only 20:55"]);
    await fireEvent.input(screen.getByLabelText("Answer @rio"), { target: { value: "both" } });
    await fireEvent.click(screen.getByLabelText("Send answer"));
    expect(onAnswer).toHaveBeenCalledWith("q", "both");
  });

  test("a failed answer shows its error and keeps the box", async () => {
    const onAnswer = vi.fn().mockRejectedValue(new Error("network down"));
    render(TasksPanel, { props: { tasks, onAnswer } });
    await fireEvent.click(within(rowOf("Rio")).getByText("Answer"));
    await fireEvent.input(screen.getByLabelText("Answer @rio"), { target: { value: "both" } });
    await fireEvent.click(screen.getByLabelText("Send answer"));
    expect(await screen.findByText("network down")).toBeDefined();
    expect(screen.getByTestId("answer-box")).toBeDefined();
  });

  test("Cancel on a question asks first", async () => {
    const onCancel = vi.fn().mockResolvedValue(undefined);
    render(TasksPanel, { props: { tasks, onCancel } });
    await fireEvent.click(within(rowOf("Rio")).getByText("Cancel"));
    expect(onCancel).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByTestId("cancel-confirm-yes"));
    expect(onCancel).toHaveBeenCalledWith("q");
  });

  test("the timer ticks while a task runs and is cleared on unmount", async () => {
    vi.useRealTimers();
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 9, 10, 12, 0, 0));
    const { unmount } = render(TasksPanel, { props: { tasks: [task({ task_id: "w" })] } });
    await tick();
    const time = () => screen.getByTestId("tasks-row-time").textContent;
    expect(time()).toBe("2m 10s");
    vi.advanceTimersByTime(1000);
    await tick();
    expect(time()).toBe("2m 11s");
    expect(vi.getTimerCount()).toBe(2); // the 1s interval + the midnight timeout
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  test("with nothing running only the midnight timeout is set, and Done today folds into Earlier at 00:00", async () => {
    vi.useRealTimers();
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 9, 10, 23, 59, 58));
    render(TasksPanel, { props: { tasks: [task({ task_id: "d", state: "completed", updated_at: at(10, 8) })] } });
    await tick();
    expect(vi.getTimerCount()).toBe(1);
    expect(screen.getByTestId("tasks-group-done")).toBeDefined();
    vi.advanceTimersByTime(3000);
    await tick();
    expect(screen.queryByTestId("tasks-group-done")).toBeNull();
    expect(screen.getByTestId("tasks-earlier-toggle").textContent).toContain("Earlier · 1");
    expect(screen.getByTestId("tasks-earlier-toggle").getAttribute("aria-controls")).toBe("tasks-earlier-rows");
  });

  test("the question shown is the one the quick answers come from (reply)", async () => {
    const q = task({ task_id: "q", to_handle: "rio", to_name: "Rio", state: "input_required", needs_you: true, summary: "Which env?", reply: "Which env?\nPick one (prod / staging)" });
    render(TasksPanel, { props: { tasks: [q], onAnswer: vi.fn().mockResolvedValue(undefined) } });
    const shown = screen.getByTestId("tasks-row-question");
    expect(shown.textContent).toBe("Which env?\nPick one (prod / staging)");
    expect(shown.getAttribute("title")).toBe("Which env?\nPick one (prod / staging)");
    await fireEvent.click(screen.getByText("Answer"));
    expect(screen.getAllByTestId("quick-pill").map((p) => p.textContent)).toEqual(["prod", "staging"]);
  });

  test("the answer box and the cancel confirm close once the task moves on", async () => {
    const q = task({ task_id: "q", to_handle: "rio", to_name: "Rio", state: "input_required", needs_you: true, summary: "Which env?" });
    const fn = () => vi.fn().mockResolvedValue(undefined);
    const { rerender } = render(TasksPanel, { props: { tasks: [q], onAnswer: fn(), onCancel: fn() } });
    await fireEvent.click(screen.getByText("Answer"));
    await fireEvent.click(screen.getByText("Cancel"));
    expect(screen.getByTestId("answer-box")).toBeDefined();
    expect(screen.getByTestId("cancel-confirm-yes")).toBeDefined();
    await rerender({ tasks: [{ ...q, state: "completed", needs_you: false }], onAnswer: fn(), onCancel: fn() });
    expect(screen.queryByTestId("answer-box")).toBeNull();
    expect(screen.queryByTestId("cancel-confirm-yes")).toBeNull();
  });

  test("a row the sender is answering sits in Working with the red Cancel", () => {
    const a = task({ task_id: "a", to_handle: "vera", to_name: "Vera", state: "input_required", summary: "Which env?" });
    render(TasksPanel, { props: { tasks: [a], senderName: "Cap", onAnswer: vi.fn(), onCancel: vi.fn() } });
    expect(screen.getByTestId("tasks-group-working")).toBeDefined();
    expect(screen.getByText("Cancel").closest("button")!.dataset.variant).toBe("danger");
    expect(screen.getByText("Cap is answering…")).toBeDefined();
  });

  test("Raw payload toggles, with Jump / Open chat beside it on an ended row (not inside it)", async () => {
    const onJump = vi.fn();
    const onOpenChat = vi.fn();
    render(TasksPanel, { props: { tasks: [task({ task_id: "d", to_handle: "lia", to_name: "Lia", state: "completed", updated_at: at(10, 8) })], onJump, onOpenChat } });
    expect(document.querySelector("summary, details")).toBeNull();
    const raw = screen.getByText("Raw payload");
    expect(raw.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByTestId("tasks-row-raw")).toBeNull();
    await fireEvent.click(raw);
    expect(screen.getByTestId("tasks-row-raw").textContent).toContain('"task_id": "d"');
    await fireEvent.click(screen.getByLabelText("Jump to Lia's task"));
    await fireEvent.click(screen.getByLabelText("Open Lia's chat"));
    expect(onJump).toHaveBeenCalledTimes(1);
    expect(onOpenChat).toHaveBeenCalledTimes(1);
    expect(raw.contains(screen.getByLabelText("Jump to Lia's task"))).toBe(false);
  });

  test("header={false} drops the panel's own header", () => {
    render(TasksPanel, { props: { tasks, header: false } });
    expect(screen.queryByText("Tasks")).toBeNull();
    expect(screen.queryByTestId("tasks-panel-count")).toBeNull();
  });
});
