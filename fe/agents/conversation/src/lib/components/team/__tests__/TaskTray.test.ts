import { describe, expect, test, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import TaskTray from "../TaskTray.svelte";
import type { TeamTaskItem } from "../../../types/agents.js";

function task(o: Partial<TeamTaskItem>): TeamTaskItem {
  return {
    task_id: "t1", context_id: "c1", to_agent_id: "a1", to_handle: "anton", to_name: "Anton",
    title: "check the logs", state: "working", turns: 1, max_turns: 8,
    started_at: "2026-10-10T10:00:00Z", updated_at: "2026-10-10T10:00:00Z", age: "2m",
    ...o,
  };
}

const mixed = () => [
  task({ task_id: "a" }),
  task({ task_id: "b", to_handle: "vera", to_name: "Vera" }),
  task({ task_id: "q", to_handle: "rio", to_name: "Rio", state: "input_required", needs_you: true }),
];

describe("TaskTray (grouped §4)", () => {
  test("the container carries the mockup tokens, dark ones included", () => {
    render(TaskTray, { props: { tasks: mixed() } });
    const bar = screen.getByTestId("task-tray").firstElementChild as HTMLElement;
    for (const c of ["rounded-lg", "border-white-300", "dark:border-navy-600", "bg-white-100", "dark:bg-navy-800", "py-1.5", "pl-2.5", "pr-2", "gap-2.5", "text-xs"]) expect(bar.classList).toContain(c);
    expect(screen.getByTestId("task-tray").classList).toContain("mb-2");
  });

  test("stacks 22px avatars with the task that needs you first", () => {
    render(TaskTray, { props: { tasks: mixed() } });
    const avs = screen.getAllByTestId("task-tray-avatar");
    expect(avs.map((a) => a.dataset.status)).toEqual(["needs_you", "working", "working"]);
    expect(avs[0].parentElement!.classList).toContain("-space-x-1.5");
    for (const a of avs) expect(a.querySelector('[style*="width: 22px"], [width="22"]')).not.toBeNull();
  });

  test("a question further down the list still makes the first three avatars", () => {
    const tasks = [
      task({ task_id: "w1" }),
      task({ task_id: "w2", to_handle: "vera" }),
      task({ task_id: "w3", to_handle: "lia" }),
      task({ task_id: "w4", to_handle: "max" }),
      task({ task_id: "q5", to_handle: "rio", state: "input_required", needs_you: true }),
    ];
    render(TaskTray, { props: { tasks } });
    const avs = screen.getAllByTestId("task-tray-avatar");
    expect(avs).toHaveLength(3);
    expect(avs.map((a) => a.dataset.status)).toEqual(["needs_you", "working", "working"]);
  });

  test("the summary is statusParts in their colours, without the amber dot", () => {
    render(TaskTray, { props: { tasks: mixed() } });
    const sum = screen.getByTestId("task-tray-summary");
    expect(sum.textContent).toBe("2 working · 1 needs input");
    expect(sum.classList).toContain("dark:text-black-600");
    const working = sum.querySelector('[data-status="working"]')!;
    for (const c of ["font-semibold", "text-green-600", "dark:text-green-400"]) expect(working.classList.contains(c)).toBe(true);
    const needs = sum.querySelector('[data-status="needs_you"]')!;
    for (const c of ["font-semibold", "text-amber-700", "dark:text-amber-300"]) expect(needs.classList.contains(c)).toBe(true);
    expect(screen.getByTestId("task-tray").querySelector(".bg-amber-500")).toBeNull();
  });

  test("Answer is the warn outline SmBtn and opens the question", async () => {
    render(TaskTray, { props: { tasks: mixed(), onAnswer: vi.fn() } });
    const btn = screen.getByText("Answer").closest("button")!;
    expect(btn.dataset.variant).toBe("warn");
    for (const c of ["border-amber-500/[.45]", "text-amber-700", "dark:text-amber-300"]) expect(btn.classList.contains(c)).toBe(true);
    expect(btn.classList.contains("text-white-100")).toBe(false);
    await fireEvent.click(btn);
    expect(screen.getByLabelText("Answer @rio")).toBeDefined();
  });

  test("no Answer while nothing needs you", () => {
    render(TaskTray, { props: { tasks: [task({})], onAnswer: vi.fn() } });
    expect(screen.queryByText("Answer")).toBeNull();
  });

  test("the chevron points up while closed, turns 180° open, and reports aria-expanded/-controls", async () => {
    render(TaskTray, { props: { tasks: mixed() } });
    const chev = screen.getByTestId("task-tray-toggle");
    const icon = () => chev.querySelector("svg")!.getAttribute("class") ?? "";
    expect(chev.getAttribute("aria-expanded")).toBe("false");
    expect(chev.getAttribute("aria-label")).toBe("Show tasks");
    // The list opens upward: closed, the chevron points up.
    expect(icon()).toContain("rotate-180");
    expect(chev.hasAttribute("aria-controls")).toBe(false);
    expect(screen.queryByTestId("task-tray-popover")).toBeNull();
    await fireEvent.click(chev);
    expect(chev.getAttribute("aria-expanded")).toBe("true");
    expect(chev.getAttribute("aria-label")).toBe("Hide tasks");
    expect(icon()).not.toContain("rotate-180");
    const list = screen.getByTestId("task-tray-popover");
    expect(list.id).not.toBe("");
    expect(chev.getAttribute("aria-controls")).toBe(list.id);
    const summaryBtn = screen.getByTestId("task-tray-summary").closest("button")!;
    expect(summaryBtn.getAttribute("aria-controls")).toBe(list.id);
    expect(screen.getAllByTestId("task-tray-name")).toHaveLength(3);
    await fireEvent.click(chev);
    expect(screen.queryByTestId("task-tray-popover")).toBeNull();
    expect(chev.hasAttribute("aria-controls")).toBe(false);
    expect(chev.classList).toContain("dark:hover:text-white-100");
  });
});
