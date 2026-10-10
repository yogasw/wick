import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import { tick } from "svelte";
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
    expect(screen.getByTestId("delegation-summary").textContent).toBe("· 2 working · 1 needs input · 1 replied");
    expect(screen.getAllByTestId("delegation-chip")).toHaveLength(4);
  });

  test("needs you opens the answer card with quick replies; the answer goes to the task", async () => {
    const onAnswer = vi.fn().mockResolvedValue(undefined);
    render(DelegationBlock, {
      props: { tasks: [task({ state: "input_required", needs_you: true, reply: "Which env? (prod / staging)" })], captainName: "Cap", onAnswer },
    });
    expect(screen.getByTestId("needs-you-card")).toBeDefined();
    expect(screen.getByTestId("answer-to").textContent).toBe("Answer goes to @anton · Cap sees it too");
    await fireEvent.click(screen.getByText("prod"));
    expect(onAnswer).toHaveBeenCalledWith("t1", "prod");
  });

  test("2a: the needs-input section sits in the block, head + indented body + AnswerBox", async () => {
    let done!: () => void;
    const onAnswer = vi.fn(() => new Promise<void>((r) => { done = r; }));
    render(DelegationBlock, {
      props: { tasks: [task({ state: "input_required", needs_you: true, title: "List deploys", reply: "Diff both? (both / only 20:55)" })], captainName: "Cap", onAnswer },
    });
    const sec = screen.getByTestId("needs-you-card");
    for (const c of ["border-t", "border-white-300", "dark:border-navy-600", "bg-[rgba(251,191,36,.12)]", "dark:bg-[rgba(251,191,36,.10)]"]) expect(sec.classList.contains(c)).toBe(true);
    expect(sec.className).not.toMatch(/rounded/);
    const head = sec.firstElementChild as HTMLElement;
    expect(head.className).toContain("py-[7px]");
    expect(head.className).toContain("text-[11.5px]");
    expect(head.querySelector("b")!.textContent).toBe("Anton");
    // No asked_at yet: the head stops at the title.
    expect(screen.getByTestId("needs-you-task").textContent).toBe("· List deploys");
    expect((sec.children[1] as HTMLElement).className).toContain("pl-[38px]");
    const q = screen.getByTestId("needs-you-question");
    expect(q.className).toContain("text-[13px]");
    expect(q.textContent).toBe("Diff both? (both / only 20:55)");
    expect(screen.getAllByTestId("quick-pill").map((p) => p.textContent)).toEqual(["both", "only 20:55"]);
    const input = screen.getByLabelText("Answer @anton") as HTMLInputElement;
    expect(input.placeholder).toBe("Type an answer…");
    const btn = screen.getByLabelText("Send answer") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    await fireEvent.input(input, { target: { value: "both, please" } });
    expect(btn.disabled).toBe(false);
    await fireEvent.click(btn);
    expect(onAnswer).toHaveBeenCalledWith("t1", "both, please");
    // While it is on its way the button spins and stays disabled.
    await tick();
    expect(btn.disabled).toBe(true);
    expect(btn.querySelector("svg")!.dataset.icon).toBe("spin");
    expect((screen.getAllByTestId("quick-pill")[0] as HTMLButtonElement).disabled).toBe(true);
    done();
    await vi.waitFor(() => expect((screen.getByLabelText("Answer @anton") as HTMLInputElement).value).toBe(""));
  });

  test("2a: the head reads asked HH:MM once the task carries asked_at", () => {
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date(2026, 9, 10, 10, 14, 10));
      const askedAt = new Date(2026, 9, 10, 10, 12, 0).toISOString();
      render(DelegationBlock, {
        props: { tasks: [task({ state: "input_required", needs_you: true, title: "List deploys", reply: "Which env?", asked_at: askedAt })], onAnswer: vi.fn() },
      });
      expect(screen.getByTestId("needs-you-task").textContent).toBe("· List deploys · asked 10:12");
    } finally {
      vi.useRealTimers();
    }
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
    expect(screen.getByTestId("fold-title").textContent).toBe("Delegated to 2 teammates");
    expect(screen.getByTestId("fold-summary").textContent).toContain("· all replied · 3 messages");
    await fireEvent.click(screen.getByTestId("delegation-folded"));
    expect(screen.getByTestId("delegation-block")).toBeDefined();
  });

  test("§3 fold row: full-width button, check, one avatar per teammate, title, summary with duration, chevron", () => {
    render(DelegationBlock, {
      props: {
        tasks: [
          task({ task_id: "a", to_agent_id: "a1", state: "completed", turns: 5, started_at: "2026-10-10T09:31:00Z", updated_at: "2026-10-10T09:40:00Z" }),
          task({ task_id: "b", to_agent_id: "a1", state: "completed", turns: 3, started_at: "2026-10-10T09:33:00Z", updated_at: "2026-10-10T09:38:00Z" }),
          task({ task_id: "c", to_agent_id: "a2", to_handle: "vera", to_name: "Vera", state: "completed", turns: 4, started_at: "2026-10-10T09:32:00Z", updated_at: "2026-10-10T09:44:00Z" }),
        ],
      },
    });
    const row = screen.getByTestId("delegation-folded");
    expect(row.tagName).toBe("BUTTON");
    expect(row.getAttribute("type")).toBe("button");
    expect(row.getAttribute("aria-expanded")).toBe("false");
    for (const c of ["flex", "w-full", "rounded-xl", "border", "border-white-300", "dark:border-navy-600", "bg-white-100", "dark:bg-navy-800", "px-3", "py-[7px]"]) expect(row.classList.contains(c)).toBe(true);
    expect(row.className).not.toMatch(/rounded-full/);
    const check = row.querySelector('[data-icon="check"]')!;
    expect(check.getAttribute("width")).toBe("14");
    expect(check.getAttribute("class")).toContain("text-green-600");
    expect(screen.getByTestId("fold-avatars").getAttribute("aria-hidden")).toBe("true");
    const avs = screen.getAllByTestId("fold-avatar");
    expect(avs).toHaveLength(2);
    expect(avs[0].className).not.toContain("-ml-[5px]");
    expect(avs[1].className).toContain("-ml-[5px]");
    const title = screen.getByTestId("fold-title");
    expect(title.textContent).toBe("Delegated to 3 teammates");
    for (const c of ["font-medium", "text-xs", "text-black-800", "dark:text-black-600"]) expect(title.classList.contains(c)).toBe(true);
    const sum = screen.getByTestId("fold-summary");
    expect(sum.textContent!.replace(/\s+/g, " ").trim()).toBe("· all replied · 12 messages · 13m");
    expect(sum.querySelector("b")!.className).toContain("text-green-600");
    expect(sum.classList.contains("dark:text-black-600")).toBe(true);
    const chev = row.querySelector('[data-icon="chevron"]')!.getAttribute("class")!;
    expect(chev).toContain("-rotate-90");
    expect(chev).toContain("dark:text-black-600");
  });

  test("§3 fold row: a declined task shows in red instead of all replied; the chevron opens the full block", async () => {
    render(DelegationBlock, {
      props: {
        tasks: [
          task({ task_id: "a", state: "completed", turns: 2, started_at: "2026-10-10T09:00:00Z", updated_at: "2026-10-10T09:30:00Z" }),
          task({ task_id: "b", to_agent_id: "a2", to_handle: "vera", state: "rejected", turns: 1, started_at: "2026-10-10T09:00:00Z", updated_at: "2026-10-10T10:05:00Z" }),
        ],
      },
    });
    const sum = screen.getByTestId("fold-summary");
    expect(sum.textContent!.replace(/\s+/g, " ").trim()).toBe("· 1 replied · 1 declined · 3 messages · 1h 5m");
    expect(sum.querySelector("b")).toBeNull();
    expect(sum.querySelector('[data-status="declined"]')!.className).toContain("text-red-600");
    screen.getByTestId("delegation-folded").focus();
    await fireEvent.click(screen.getByTestId("delegation-folded"));
    expect(screen.queryByTestId("delegation-folded")).toBeNull();
    expect(screen.getAllByTestId("delegation-chip")).toHaveLength(2);
    // Focus moves to the open block's toggle, not to <body>.
    await vi.waitFor(() => expect(document.activeElement).toBe(screen.getByTestId("delegation-collapse")));
  });

  test("a failure is never folded behind all replied", () => {
    render(DelegationBlock, { props: { tasks: [task({ task_id: "a", state: "completed" }), task({ task_id: "b", state: "failed" })] } });
    expect(screen.getByTestId("delegation-folded").textContent).toContain("1 replied · 1 failed");
    expect(screen.getByTestId("fold-summary").querySelector('[data-status="failed"]')!.className).toContain("text-red-600");
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

/* P48 slice 3: the head and the chips as grouped §2 draws them. */
describe("DelegationBlock head + chip (P48 s3)", () => {
  afterEach(() => { vi.useRealTimers(); });
  const fake = (iso: string) => { vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date"] }); vi.setSystemTime(new Date(iso)); };
  const chip = (name: string) => screen.getAllByTestId("delegation-chip").find((c) => c.textContent!.includes(name))!;
  const four = () => [
    task({ task_id: "w1", to_handle: "halodev", to_name: "Halodev", started_at: "2026-10-10T10:02:00Z" }),
    task({ task_id: "w2", to_handle: "wabster", to_name: "Wabster", started_at: "2026-10-10T10:03:30Z" }),
    task({ task_id: "q", to_handle: "franky", to_name: "Franky", state: "input_required", needs_you: true, reply: "Diff both?", updated_at: "2026-10-10T10:11:50Z" }),
    task({ task_id: "r", to_handle: "atraxa", to_name: "Atraxa", state: "completed", summary: "done", updated_at: "2026-10-10T10:08:00Z" }),
  ];

  test("head: tasks icon, 12.5px title, coloured summary parts, Show all as a small button", async () => {
    render(DelegationBlock, { props: { tasks: four() } });
    const block = screen.getByTestId("delegation-block");
    expect(block.className).toContain("overflow-hidden");
    expect(block.className).not.toMatch(/(^|\s)px-3(\s|$)/);
    expect(block.querySelector("svg[data-icon=tasks]")!.getAttribute("width")).toBe("14");
    expect(screen.getByText("Delegated to 4 teammates").className).toContain("text-[12.5px]");
    const part = (st: string) => screen.getByTestId("delegation-summary").querySelector<HTMLElement>(`[data-status=${st}]`)!;
    expect(part("working").className).toContain("text-green-600");
    expect(part("needs_you").className).toContain("text-amber-700");
    expect(part("replied").className).toContain("text-black-800");
    const all = screen.getByTestId("delegation-show-all");
    expect(all.dataset.variant).toBe("default");
    expect(all.textContent!.trim()).toBe("Show all");
    await fireEvent.click(all);
    expect(all.textContent!.trim()).toBe("Hide");
    expect(screen.getAllByTestId("delegation-preview")).toHaveLength(4);
  });

  test("failed and declined read red, canceled muted", () => {
    render(DelegationBlock, { props: { tasks: [task({ task_id: "w" }), task({ task_id: "f", state: "failed" }), task({ task_id: "d", state: "rejected" }), task({ task_id: "c", state: "canceled" })] } });
    const sum = screen.getByTestId("delegation-summary");
    expect(sum.textContent).toBe("· 1 working · 1 declined · 1 failed · 1 canceled");
    for (const st of ["failed", "declined"]) expect(sum.querySelector<HTMLElement>(`[data-status=${st}]`)!.className).toContain("dark:text-red-400");
    expect(sum.querySelector<HTMLElement>("[data-status=canceled]")!.className).toBe("text-black-700");
    const status = (s: string) => screen.getAllByTestId("delegation-chip").find((c) => c.dataset.status === s)!.querySelector<HTMLElement>("[data-testid=chip-status]")!;
    expect(status("failed").className).toContain("text-red-600");
    expect(status("declined").className).toContain("text-red-600");
    expect(status("declined").textContent).toBe("declined");
    expect(status("canceled").className).toContain("text-black-700");
  });

  test("the chevron collapses the block to its chips and back", async () => {
    render(DelegationBlock, { props: { tasks: four(), onAnswer: vi.fn() } });
    await fireEvent.click(chip("Halodev"));
    expect(screen.getByTestId("needs-you-card")).toBeDefined();
    expect(screen.getByTestId("delegation-preview")).toBeDefined();
    const chev = screen.getByTestId("delegation-collapse");
    expect(chev.getAttribute("aria-expanded")).toBe("true");
    await fireEvent.click(chev);
    expect(chev.getAttribute("aria-expanded")).toBe("false");
    expect(chev.getAttribute("aria-label")).toBe("Expand");
    expect(chev.querySelector("svg")!.getAttribute("class")).toContain("-rotate-90");
    expect(screen.getAllByTestId("delegation-chip")).toHaveLength(4);
    expect(screen.queryByTestId("needs-you-card")).toBeNull();
    expect(screen.queryByTestId("delegation-preview")).toBeNull();
    expect(chip("Halodev").dataset.open).toBe("false");
    await fireEvent.click(chev);
    expect(screen.getByTestId("needs-you-card")).toBeDefined();
    expect(screen.getByTestId("delegation-preview")).toBeDefined();
  });

  test("a chip on a collapsed block opens it on that chip, even the one already selected", async () => {
    render(DelegationBlock, { props: { tasks: four() } });
    await fireEvent.click(chip("Halodev"));
    expect(chip("Halodev").getAttribute("aria-expanded")).toBe("true");
    await fireEvent.click(screen.getByTestId("delegation-collapse"));
    expect(chip("Halodev").getAttribute("aria-expanded")).toBe("false");
    await fireEvent.click(chip("Halodev"));
    expect(screen.getByTestId("delegation-collapse").getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByTestId("delegation-preview").textContent).toContain("@halodev");
    expect(chip("Halodev").dataset.open).toBe("true");
    // Open, a second click still toggles it off.
    await fireEvent.click(chip("Halodev"));
    expect(screen.queryByTestId("delegation-preview")).toBeNull();
    expect(chip("Franky").getAttribute("aria-expanded")).toBe("false");
  });

  test("Show all never reads Hide while collapsed; a click there opens everything", async () => {
    render(DelegationBlock, { props: { tasks: four() } });
    const all = screen.getByTestId("delegation-show-all");
    await fireEvent.click(all);
    expect(all.getAttribute("aria-expanded")).toBe("true");
    await fireEvent.click(screen.getByTestId("delegation-collapse"));
    expect(all.textContent!.trim()).toBe("Show all");
    expect(all.getAttribute("aria-expanded")).toBe("false");
    await fireEvent.click(all);
    expect(all.textContent!.trim()).toBe("Hide");
    expect(screen.getAllByTestId("delegation-preview")).toHaveLength(4);
  });

  test("the head chevron has a 24px hit area and a focus ring", () => {
    render(DelegationBlock, { props: { tasks: four() } });
    const c = screen.getByTestId("delegation-collapse").className;
    for (const k of ["h-6", "w-6", "-m-1.5", "focus-visible:ring-1"]) expect(c).toContain(k);
  });

  test("chips: spin while working, check when replied, needs input with no chevron; open is green", async () => {
    fake("2026-10-10T10:12:00Z");
    render(DelegationBlock, { props: { tasks: four(), captainName: "Captain" } });
    const w = chip("Halodev");
    expect(w.className).toContain("text-[11.5px]");
    expect(w.className).toContain("bg-white-200");
    expect(w.querySelector<SVGElement>("svg[data-icon=spin]")!.classList.contains("team-icon-spin")).toBe(true);
    expect(w.querySelector("[data-testid=chip-status]")!.textContent).toBe("working");
    expect(w.querySelector("[data-testid=avatar-activity]")).not.toBeNull();
    const r = chip("Atraxa");
    expect(r.querySelector("svg[data-icon=check]")).not.toBeNull();
    expect(r.querySelector("[data-testid=chip-status]")!.textContent).toBe("replied");
    const q = chip("Franky");
    expect(q.querySelector("[data-testid=chip-status]")!.textContent).toBe("needs input");
    expect(q.className).toContain("border-amber-500/[.45]");
    expect(q.className).toContain("bg-[rgba(251,191,36,.12)]");
    expect(q.querySelector("svg[data-icon=chevron]")).toBeNull();
    expect(w.querySelector("svg[data-icon=chevron]")!.getAttribute("class")).toContain("-rotate-90");
    await fireEvent.click(w);
    expect(w.dataset.open).toBe("true");
    expect(w.className).toContain("border-green-500");
    expect(w.className).toContain("ring-1");
    expect(w.querySelector("svg[data-icon=chevron]")!.getAttribute("class")).not.toContain("-rotate-90");
    expect(chip("Wabster").className).not.toContain("ring-1");
  });

  test("elapsed: a running timer, how long a question waited, the clock time a reply came", async () => {
    fake("2026-10-10T10:12:00Z");
    render(DelegationBlock, { props: { tasks: four() } });
    const el = (name: string) => chip(name).querySelector("[data-testid=chip-elapsed]")!.textContent;
    expect(el("Halodev")).toBe("10m 00s");
    expect(el("Wabster")).toBe("8m 30s");
    expect(el("Franky")).toBe("10s");
    const d = new Date("2026-10-10T10:08:00Z");
    expect(el("Atraxa")).toBe(`${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`);
    expect(chip("Atraxa").querySelector("[data-testid=chip-elapsed]")!.className).toContain("font-mono");
    await vi.advanceTimersByTimeAsync(1000);
    await tick();
    expect(el("Halodev")).toBe("10m 01s");
    expect(el("Franky")).toBe("11s");
  });

  test("the 1s tick runs only while a task is active and stops on destroy", () => {
    fake("2026-10-10T10:12:00Z");
    const settled = render(DelegationBlock, { props: { tasks: [task({ state: "completed" }), task({ task_id: "f", state: "failed" })] } });
    expect(vi.getTimerCount()).toBe(0);
    settled.unmount();
    const live = render(DelegationBlock, { props: { tasks: four() } });
    expect(vi.getTimerCount()).toBe(1);
    live.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  test("the tick stops once the tasks finish (props update)", async () => {
    fake("2026-10-10T10:12:00Z");
    const r = render(DelegationBlock, { props: { tasks: [task({ task_id: "p1" })] } });
    expect(vi.getTimerCount()).toBe(1);
    await r.rerender({ tasks: [task({ task_id: "p1", state: "completed" })] });
    expect(vi.getTimerCount()).toBe(0);
    await r.rerender({ tasks: [task({ task_id: "p1" })] });
    expect(vi.getTimerCount()).toBe(1);
    r.unmount();
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
    expect(screen.getByTestId("chip-status").textContent).toBe("interrupted by a restart");
    expect(screen.getByTestId("chip-status").className).toContain("text-red-600");
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
  test("groups Needs input / Working / Done today / Earlier / Failed · Canceled with a raw payload", () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 9, 10, 12, 0));
    try {
      render(TasksPanel, {
        props: {
          tasks: [
            task({ task_id: "a", state: "input_required", needs_you: true }),
            task({ task_id: "b" }),
            task({ task_id: "c", state: "completed", updated_at: new Date(2026, 9, 10, 9, 0).toISOString() }),
            task({ task_id: "e", state: "completed", updated_at: new Date(2026, 9, 9, 18, 0).toISOString() }),
            task({ task_id: "d", state: "canceled" }),
          ],
        },
      });
      for (const g of ["needs_you", "working", "done", "earlier", "failed"]) expect(screen.getByTestId("tasks-group-" + g)).toBeDefined();
      expect(screen.getByText("needs input · 2m")).toBeDefined();
      expect(screen.getAllByText("Raw payload")).toHaveLength(5);
    } finally {
      vi.useRealTimers();
    }
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
    await fireEvent.click(screen.getByLabelText("Send answer"));
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
    expect(chip.textContent).toContain("needs input");
    expect(chip.textContent).not.toContain("is answering");
    expect(screen.queryByTestId("answer-instead")).toBeNull();
    expect(screen.getByTestId("needs-you-card")).toBeDefined();
    expect(screen.getByTestId("answer-to").textContent).toBe("Answer goes to @anton · you asked, so only you answer");
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
