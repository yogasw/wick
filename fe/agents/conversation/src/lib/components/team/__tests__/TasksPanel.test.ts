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

  test("B1: asked_at times the question, ended_at places and times the row, declined reads red", () => {
    render(TasksPanel, {
      props: {
        tasks: [
          task({ task_id: "q", to_handle: "rio", to_name: "Rio", state: "input_required", needs_you: true, asked_at: at(11, 50), updated_at: at(11, 59), summary: "Which env?" }),
          task({ task_id: "d", to_handle: "lia", to_name: "Lia", state: "completed", ended_at: at(10, 8), updated_at: at(11, 30) }),
          task({ task_id: "e", to_handle: "vera", to_name: "Vera", state: "completed", ended_at: at(18, 0, 0, 9), updated_at: at(9, 0) }),
          task({ task_id: "r", to_handle: "kai", to_name: "Kai", state: "rejected", ended_at: at(11, 40), summary: "not my job" }),
        ],
      },
    });
    expect(within(rowOf("Rio")).getByTestId("tasks-row-time").textContent).toBe("10m 00s");
    expect(within(screen.getByTestId("tasks-group-done")).getAllByTestId("tasks-panel-name").map((n) => n.textContent)).toEqual(["Lia"]);
    expect(within(rowOf("Lia")).getByTestId("tasks-row-time").textContent).toBe("10:08");
    expect(within(screen.getByTestId("tasks-group-earlier")).getByTestId("tasks-earlier-toggle")).toBeDefined();
    const r = within(screen.getByTestId("tasks-group-failed")).getByTestId("tasks-row");
    const tag = within(r).getByTestId("tasks-row-tag");
    expect(tag.textContent).toBe("declined");
    expect(tag.className).toContain("text-red-600");
    expect(tag.className).toContain("dark:text-red-400");
    expect(within(r).getByTestId("tasks-row-time").textContent).toBe("11:40");
    expect(within(r).getByText("not my job")).toBeDefined();
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

  test("s18c: inline code in the question, escaped, and the teammate's choices as pills", async () => {
    const q = task({ task_id: "q", to_handle: "rio", to_name: "Rio", state: "input_required", needs_you: true, reply: "Use `v2.14.0`? <img src=x onerror=alert(1)> (yes / no)", options: ["Both deploys", "Only 20:55"] });
    render(TasksPanel, { props: { tasks: [q], onAnswer: vi.fn().mockResolvedValue(undefined) } });
    const shown = screen.getByTestId("tasks-row-question");
    expect(shown.querySelector("code")!.textContent).toBe("v2.14.0");
    expect(shown.querySelector("img")).toBeNull();
    expect(shown.textContent).toBe("Use v2.14.0? <img src=x onerror=alert(1)> (yes / no)");
    await fireEvent.click(screen.getByText("Answer"));
    expect(screen.getAllByTestId("quick-pill").map((p) => p.textContent)).toEqual(["Both deploys", "Only 20:55"]);
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

/* P48 s11 (A.10): a working row's "Thread · N" opens its TaskThread. */
describe("TasksPanel Thread · N", () => {
  test("Thread · N opens the task's thread under the row, read lazily", async () => {
    const loadThread = vi.fn().mockResolvedValue([
      { role: "captain", handle: "captain", name: "Captain", text: "check the logs", at: at(11, 57) },
      { role: "teammate", handle: "anton", name: "Anton", text: "on it", at: at(11, 58) },
    ]);
    render(TasksPanel, { props: { tasks: [task({ message_count: 2 }), task({ task_id: "d", to_handle: "lia", to_name: "Lia", state: "completed" })], loadThread, onCancel: vi.fn() } });
    const btns = screen.getAllByTestId("tasks-row-thread");
    expect(btns).toHaveLength(1);
    expect(btns[0].textContent!.trim()).toBe("Thread · 2");
    expect(loadThread).not.toHaveBeenCalled();
    await fireEvent.click(btns[0]);
    expect(btns[0].getAttribute("aria-expanded")).toBe("true");
    await vi.waitFor(() => expect(within(rowOf("Anton")).getAllByTestId("task-thread-msg")).toHaveLength(2));
    expect(loadThread).toHaveBeenCalledWith("t1");
    await fireEvent.click(btns[0]);
    expect(screen.queryByTestId("task-thread")).toBeNull();
  });

  test("without message_count it reads Thread; without a loader it is not offered", async () => {
    const r = render(TasksPanel, { props: { tasks: [task({})], loadThread: vi.fn().mockResolvedValue([]) } });
    expect(screen.getByTestId("tasks-row-thread").textContent!.trim()).toBe("Thread");
    r.unmount();
    render(TasksPanel, { props: { tasks: [task({})], onCancel: vi.fn() } });
    expect(screen.queryByTestId("tasks-row-thread")).toBeNull();
  });

  /* P48 s13 (B3): a working row's t3 is the teammate's live progress, in
     green, one line; no other row shows it. */
  test("working row shows the live progress line; other rows do not", () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 9, 10, 12, 0, 0));
    const long = "Pulling message_logs for channel 0812 " + "x".repeat(200);
    render(TasksPanel, {
      props: {
        tasks: [
          task({ task_id: "w", to_handle: "anton", to_name: "Anton", progress: "Checking Loki for the 21:00 window…" }),
          task({ task_id: "v", to_handle: "vera", to_name: "Vera", progress: long + "\nsecond line" }),
          task({ task_id: "q", to_handle: "rio", to_name: "Rio", state: "input_required", needs_you: true, summary: "Diff both?", progress: "stale" }),
          task({ task_id: "d", to_handle: "lia", to_name: "Lia", state: "completed", updated_at: at(11, 0), progress: "stale" }),
          task({ task_id: "x", to_handle: "kai", to_name: "Kai", state: "failed", summary: "No reply", progress: "stale" }),
        ],
      },
    });
    const p = within(rowOf("Anton")).getByTestId("tasks-row-progress");
    expect(p.textContent).toBe("Checking Loki for the 21:00 window…");
    expect(p.className).toContain("text-green-600");
    expect(p.className).toContain("truncate");
    const v = within(rowOf("Vera")).getByTestId("tasks-row-progress").textContent!;
    expect(Array.from(v)).toHaveLength(140);
    expect(v.endsWith("…")).toBe(true);
    expect(v).not.toContain("\n");
    for (const name of ["Rio", "Lia", "Kai"]) expect(within(rowOf(name)).queryByTestId("tasks-row-progress")).toBeNull();
    expect(screen.getAllByTestId("tasks-row-progress")).toHaveLength(2);
    vi.useRealTimers();
  });

  test("a working row with no progress yet shows no t3", () => {
    render(TasksPanel, { props: { tasks: [task({})] } });
    expect(screen.queryByTestId("tasks-row-progress")).toBeNull();
  });

  test("markup is plain text, never HTML", () => {
    render(TasksPanel, { props: { tasks: [task({ progress: "<b>bold</b> **md**" })] } });
    const p = screen.getByTestId("tasks-row-progress");
    expect(p.textContent).toBe("<b>bold</b> **md**");
    expect(p.querySelector("b")).toBeNull();
  });
});

/* P48 s14 (B6): a failed row offers Retry, a declined one Reassign with a
   menu of this chat's other teammates; once sent again the row links to
   the new task. Read-only chats pass no handlers and get no buttons. */
describe("TasksPanel Retry / Reassign", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 9, 10, 12, 0, 0));
  });
  afterEach(() => vi.useRealTimers());

  const failed = task({ task_id: "f", to_handle: "kai", to_name: "Kai", state: "failed", summary: "No reply in 10 min", updated_at: at(11, 50) });
  const declined = task({ task_id: "x", to_handle: "jules", to_name: "Google Jules", state: "rejected", summary: "No access to qiscus/raya-webhook", updated_at: at(11, 52) });
  const teammates = [
    { handle: "jules", name: "Google Jules" },
    { handle: "anton", name: "Anton" },
    { handle: "vera", name: "Vera" },
  ];

  test("failed → Retry, declined → Reassign, on the first line instead of the time", () => {
    render(TasksPanel, { props: { tasks: [failed, declined], teammates, onRetry: vi.fn(), onReassign: vi.fn() } });
    const f = rowOf("Kai");
    const retry = within(f).getByTestId("tasks-row-retry");
    expect(retry.textContent!.trim()).toBe("Retry");
    expect(retry.querySelector("svg")).not.toBeNull();
    expect(within(f).queryByTestId("tasks-row-time")).toBeNull();
    expect(within(f).queryByTestId("tasks-row-reassign")).toBeNull();
    const x = rowOf("Google Jules");
    expect(within(x).getByTestId("tasks-row-tag").textContent).toBe("declined");
    expect(within(x).getByTestId("tasks-row-reassign").textContent!.trim()).toBe("Reassign");
    expect(within(x).queryByTestId("tasks-row-retry")).toBeNull();
  });

  test("Retry calls onRetry once, shows Retrying… and blocks a second click", async () => {
    let done!: () => void;
    const onRetry = vi.fn(() => new Promise<void>((r) => { done = r; }));
    render(TasksPanel, { props: { tasks: [failed], onRetry } });
    const btn = within(rowOf("Kai")).getByTestId("tasks-row-retry") as HTMLButtonElement;
    await fireEvent.click(btn);
    expect(onRetry).toHaveBeenCalledWith("f");
    expect(btn.textContent!.trim()).toBe("Retrying…");
    expect(btn.disabled).toBe(true);
    await fireEvent.click(btn);
    expect(onRetry).toHaveBeenCalledTimes(1);
    done();
    await tick(); await tick();
    expect(btn.textContent!.trim()).toBe("Retry");
  });

  test("a failed Retry shows its error under the row", async () => {
    const onRetry = vi.fn(async () => { throw new Error("this task was already retried or reassigned"); });
    render(TasksPanel, { props: { tasks: [failed], onRetry } });
    await fireEvent.click(within(rowOf("Kai")).getByTestId("tasks-row-retry"));
    await tick(); await tick();
    expect(within(rowOf("Kai")).getByTestId("tasks-row-error").textContent).toBe("this task was already retried or reassigned");
  });

  test("Reassign opens a menu of the other teammates and sends to the one picked", async () => {
    const onReassign = vi.fn(async () => {});
    render(TasksPanel, { props: { tasks: [declined], teammates, onReassign } });
    const btn = within(rowOf("Google Jules")).getByTestId("tasks-row-reassign");
    expect(screen.queryByTestId("tasks-reassign-menu")).toBeNull();
    await fireEvent.click(btn);
    expect(btn.getAttribute("aria-expanded")).toBe("true");
    const options = screen.getAllByTestId("tasks-reassign-option");
    // The current assignee is not offered.
    expect(options.map((o) => o.textContent!.replace(/\s+/g, " ").trim())).toEqual(["Anton @anton", "Vera @vera"]);
    await fireEvent.click(options[1]);
    expect(onReassign).toHaveBeenCalledWith("x", "vera");
    expect(screen.queryByTestId("tasks-reassign-menu")).toBeNull();
  });

  test("Escape closes the menu", async () => {
    render(TasksPanel, { props: { tasks: [declined], teammates, onReassign: vi.fn() } });
    await fireEvent.click(within(rowOf("Google Jules")).getByTestId("tasks-row-reassign"));
    expect(screen.getByTestId("tasks-reassign-menu")).toBeDefined();
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByTestId("tasks-reassign-menu")).toBeNull();
  });

  test("no other teammate: Reassign is disabled with a tooltip", () => {
    render(TasksPanel, { props: { tasks: [declined], teammates: [{ handle: "jules", name: "Google Jules" }], onReassign: vi.fn() } });
    const btn = within(rowOf("Google Jules")).getByTestId("tasks-row-reassign") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.getAttribute("title")).toBe("No other teammate in this chat");
  });

  test("read-only chat: no Retry, no Reassign", () => {
    render(TasksPanel, { props: { tasks: [failed, declined], teammates } });
    expect(screen.queryByTestId("tasks-row-retry")).toBeNull();
    expect(screen.queryByTestId("tasks-row-reassign")).toBeNull();
    expect(within(rowOf("Kai")).getByTestId("tasks-row-time")).toBeDefined();
  });

  test("sent again: the old row links to the new task, no button", async () => {
    const retried = { ...failed, replaced_by: "f2", replaced_how: "retry" as const, replaced_to_handle: "kai", replaced_to_name: "Kai" };
    const moved = { ...declined, replaced_by: "x2", replaced_how: "reassign" as const, replaced_to_handle: "vera", replaced_to_name: "Vera" };
    const fresh = task({ task_id: "x2", to_handle: "vera", to_name: "Vera", retry_of: "x" });
    render(TasksPanel, { props: { tasks: [retried, moved, fresh], teammates, onRetry: vi.fn(), onReassign: vi.fn(), agents: { vera: { name: "Vera V." } } } });
    expect(within(rowOf("Kai")).getByTestId("tasks-row-replaced").textContent!.trim()).toBe("Retried → Kai");
    expect(within(rowOf("Kai")).queryByTestId("tasks-row-retry")).toBeNull();
    const link = within(rowOf("Google Jules")).getByTestId("tasks-row-replaced");
    expect(link.textContent!.trim()).toBe("Reassigned → Vera V.");
    expect(within(rowOf("Google Jules")).queryByTestId("tasks-row-reassign")).toBeNull();
    const target = document.querySelector('[data-testid="tasks-row"][data-task-id="x2"]') as HTMLElement;
    target.scrollIntoView = vi.fn();
    await fireEvent.click(link);
    expect(target.scrollIntoView).toHaveBeenCalled();
  });
  test("the server's list: loaded once, shared agents in, refusing ones disabled", async () => {
    let resolve!: (v: { handle: string; name: string; shared?: boolean; refused?: boolean }[]) => void;
    const loadTargets = vi.fn(() => new Promise<{ handle: string; name: string; shared?: boolean; refused?: boolean }[]>((r) => { resolve = r; }));
    const onReassign = vi.fn(async () => {});
    render(TasksPanel, { props: { tasks: [failed, declined], loadTargets, onReassign, senderName: "Yoga Bot" } });
    expect(loadTargets).toHaveBeenCalledTimes(1);
    expect(loadTargets).toHaveBeenCalledWith("x");
    const btn = within(rowOf("Google Jules")).getByTestId("tasks-row-reassign") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.getAttribute("title")).toBe("Loading teammates…");
    resolve([{ handle: "lena", name: "Lena", shared: true }, { handle: "vera", name: "Vera", refused: true }]);
    await tick(); await tick();
    expect(btn.disabled).toBe(false);
    await fireEvent.click(btn);
    const [lena, vera] = screen.getAllByTestId("tasks-reassign-option") as HTMLButtonElement[];
    expect(lena.getAttribute("title")).toBe("Shared with you");
    expect(vera.disabled).toBe(true);
    expect(vera.getAttribute("title")).toBe("Doesn't take tasks from Yoga Bot");
    await fireEvent.click(lena);
    expect(onReassign).toHaveBeenCalledWith("x", "lena");
  });

  test("every teammate refuses: Reassign is disabled and says why", async () => {
    const loadTargets = vi.fn(async () => [{ handle: "vera", name: "Vera", refused: true }]);
    render(TasksPanel, { props: { tasks: [declined], loadTargets, onReassign: vi.fn(), senderName: "Yoga Bot" } });
    await tick(); await tick();
    const btn = within(rowOf("Google Jules")).getByTestId("tasks-row-reassign") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.getAttribute("title")).toBe("No teammate here takes tasks from Yoga Bot");
  });

  test("the server's refusal reads as is under the row", async () => {
    const onReassign = vi.fn(async () => { throw new Error("that teammate does not take tasks from this agent — pick another one"); });
    render(TasksPanel, { props: { tasks: [declined], teammates, onReassign } });
    await fireEvent.click(within(rowOf("Google Jules")).getByTestId("tasks-row-reassign"));
    await fireEvent.click(screen.getAllByTestId("tasks-reassign-option")[0]);
    await tick(); await tick();
    expect(within(rowOf("Google Jules")).getByTestId("tasks-row-error").textContent).toBe("that teammate does not take tasks from this agent — pick another one");
  });

  test("no room below the bottom row: the menu opens upward", async () => {
    render(TasksPanel, { props: { tasks: [declined], teammates, onReassign: vi.fn() } });
    const rect = (top: number, bottom: number) => ({ top, bottom, left: 0, right: 0, width: 0, height: bottom - top, x: 0, y: top, toJSON: () => ({}) }) as DOMRect;
    const scroller = document.querySelector("[data-tasks-scroll]") as HTMLElement;
    const btn = within(rowOf("Google Jules")).getByTestId("tasks-row-reassign");
    scroller.getBoundingClientRect = () => rect(0, 400);
    btn.getBoundingClientRect = () => rect(370, 390);
    await fireEvent.click(btn);
    const menu = screen.getByTestId("tasks-reassign-menu");
    expect(menu.dataset.placement).toBe("up");
    expect(menu.className).toContain("bottom-full");
    await fireEvent.click(btn);
    btn.getBoundingClientRect = () => rect(40, 60);
    await fireEvent.click(btn);
    expect(screen.getByTestId("tasks-reassign-menu").dataset.placement).toBe("down");
  });
});
