import { describe, test, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/svelte";
import TaskThread from "../TaskThread.svelte";
import type { TeamTaskItem, TeamThreadEntry } from "../../../types/agents.js";

/* P48 s11 (B2): a Team task's thread, grouped 2b.4–2b.7. */
const at = (h: number, m = 0) => new Date(2026, 9, 11, h, m).toISOString();

function task(o: Partial<TeamTaskItem> = {}): TeamTaskItem {
  return {
    task_id: "t1", context_id: "c1", to_agent_id: "a1", to_handle: "halodev", to_name: "Halodev",
    title: "check the logs", state: "working", turns: 1, max_turns: 8,
    started_at: at(10, 0), updated_at: at(10, 0), ...o,
  };
}

const log: TeamThreadEntry[] = [
  { role: "captain", handle: "captain", name: "Captain", text: "Check the webhook 401s", at: at(10, 5) },
  { role: "teammate", handle: "halodev", name: "Halodev", text: "38 hits from 20:58 to 21:06", at: at(10, 6) },
  { role: "user", text: "Include the request ids, please.", at: at(10, 8) },
];

describe("TaskThread", () => {
  test("rows read name, time and text; the person's carry initials and a You pill", async () => {
    const load = vi.fn().mockResolvedValue(log);
    render(TaskThread, { props: { task: task({ state: "completed" }), load, userName: "Test User" } });
    await waitFor(() => expect(screen.getAllByTestId("task-thread-msg")).toHaveLength(3));
    expect(load).toHaveBeenCalledWith("t1");
    const [cap, mate, you] = screen.getAllByTestId("task-thread-msg");
    expect(cap.textContent).toContain("Captain");
    expect(cap.textContent).toContain("10:05");
    expect(mate.textContent).toContain("Halodev");
    expect(mate.textContent).toContain("38 hits");
    expect(you.getAttribute("data-role")).toBe("user");
    expect(you.textContent).toContain("TU");
    expect(you.textContent).toContain("Test User");
    const pill = screen.getByTestId("task-thread-you");
    expect(pill.textContent).toBe("You");
    expect(pill.className).toContain("bg-green-500/[.18]");
    expect(pill.className).toContain("dark:text-green-400");
    expect(screen.getByTestId("task-thread").className).toContain("pb-1.5");
    expect(document.querySelector(".dots")).toBeNull();
  });

  test("working: the teammate's last line reads ok with dots", async () => {
    const load = vi.fn().mockResolvedValue(log.slice(0, 2));
    render(TaskThread, { props: { task: task(), load } });
    await waitFor(() => expect(screen.getAllByTestId("task-thread-msg")).toHaveLength(2));
    const last = screen.getAllByTestId("task-thread-msg")[1];
    expect(last.querySelector(".dots")).not.toBeNull();
    expect(last.querySelector(".text-green-600")!.textContent).toContain("38 hits");
    expect(screen.queryByTestId("task-thread-working")).toBeNull();
  });

  test("working after the person spoke: a Working line stands in", async () => {
    render(TaskThread, { props: { task: task(), load: vi.fn().mockResolvedValue(log) } });
    await waitFor(() => expect(screen.getByTestId("task-thread-working")).toBeDefined());
    expect(screen.getByTestId("task-thread-working").textContent).toContain("Halodev");
    expect(screen.getByTestId("task-thread-working").querySelector(".dots")).not.toBeNull();
    // Without a name, the person's row is the pill alone.
    const you = screen.getAllByTestId("task-thread-msg")[2];
    expect(you.querySelector("b")).toBeNull();
    // ...and a person icon stands in for the initials.
    const avatar = you.querySelector(".bg-green-600")!;
    expect(avatar.querySelector("svg")).not.toBeNull();
    expect(avatar.textContent!.trim()).toBe("");
  });

  test("reads again when the task changes, not otherwise", async () => {
    const load = vi.fn().mockResolvedValue(log.slice(0, 1));
    const r = render(TaskThread, { props: { task: task(), load } });
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
    load.mockResolvedValue(log);
    await r.rerender({ task: task({ updated_at: at(10, 9), message_count: 3 }), load });
    await waitFor(() => expect(screen.getAllByTestId("task-thread-msg")).toHaveLength(3));
    expect(load).toHaveBeenCalledTimes(2);
  });

  test("an error and an empty thread say so", async () => {
    const r = render(TaskThread, { props: { task: task({ state: "completed" }), load: vi.fn().mockRejectedValue(new Error("task not found")) } });
    await waitFor(() => expect(screen.getByTestId("task-thread-error").textContent).toBe("task not found"));
    r.unmount();
    render(TaskThread, { props: { task: task({ state: "completed" }), load: vi.fn().mockResolvedValue([]) } });
    await waitFor(() => expect(screen.getByText("No messages yet.")).toBeDefined());
  });

  /* P48 s13 (B3): the working row reads the live progress line (combined
     2a), plain text; without one it stays "Working…". */
  test("working row shows the progress line when there is one", async () => {
    const load = vi.fn().mockResolvedValue(log);
    const r = render(TaskThread, { props: { task: task({ progress: "Checking Loki for the 21:00 window… <b>x</b>" }), load } });
    await waitFor(() => expect(screen.getByTestId("task-thread-progress")).toBeDefined());
    const p = screen.getByTestId("task-thread-progress");
    expect(p.textContent).toBe("Checking Loki for the 21:00 window… <b>x</b>");
    expect(p.querySelector("b")).toBeNull();
    expect(p.className).toContain("truncate");
    r.unmount();
    render(TaskThread, { props: { task: task({}), load } });
    await waitFor(() => expect(screen.getByTestId("task-thread-working")).toBeDefined());
    expect(screen.queryByTestId("task-thread-progress")).toBeNull();
  });
});
