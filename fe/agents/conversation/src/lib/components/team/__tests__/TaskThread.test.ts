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

  /* P48 s18g (audit #12): the in-progress line reads "Halodev 10:13 ·
     Checking Loki…" with the animated dots (combined 2a), the dots only
     while the task runs. The time is when this client saw the line: a
     progress push does not move updated_at. */
  test("progress line carries the dots while running, none when done", async () => {
    const load = vi.fn().mockResolvedValue(log);
    const r = render(TaskThread, { props: { task: task({ progress: "Checking Loki for the 21:00 window…" }), load } });
    await waitFor(() => expect(screen.getByTestId("task-thread-progress")).toBeDefined());
    const dots = screen.getByTestId("task-thread-working").querySelector(".dots")!;
    expect(dots).not.toBeNull();
    expect(dots.getAttribute("aria-hidden")).toBe("true");
    // The line's own trailing ellipsis gives way to the dots; the title keeps the line as sent.
    const p = screen.getByTestId("task-thread-progress");
    expect(p.textContent).toBe("Checking Loki for the 21:00 window");
    expect(p.getAttribute("title")).toBe("Checking Loki for the 21:00 window…");
    r.unmount();
    render(TaskThread, { props: { task: task({ state: "completed", progress: "Checking Loki" }), load } });
    await waitFor(() => expect(screen.getAllByTestId("task-thread-msg")).toHaveLength(3));
    expect(screen.queryByTestId("task-thread-working")).toBeNull();
    expect(screen.queryByTestId("task-thread-progress")).toBeNull();
    expect(document.querySelector(".dots")).toBeNull();
  });

  test("the progress time is stamped when the line is first seen and when it changes", async () => {
    const now = vi.spyOn(Date, "now").mockReturnValue(new Date(2026, 9, 11, 10, 13).getTime());
    try {
      const load = vi.fn().mockResolvedValue(log);
      const r = render(TaskThread, { props: { task: task(), load } });
      await waitFor(() => expect(screen.getByTestId("task-thread-working")).toBeDefined());
      // "Working…": no progress yet, no time.
      expect(screen.queryByTestId("task-thread-working-at")).toBeNull();
      await r.rerender({ task: task({ progress: "Checking Loki" }), load });
      await waitFor(() => expect(screen.getByTestId("task-thread-working-at").textContent).toBe("10:13"));
      // Same text again (the task re-renders, updated_at moves): the time stays.
      now.mockReturnValue(new Date(2026, 9, 11, 10, 20).getTime());
      await r.rerender({ task: task({ progress: "Checking Loki", updated_at: at(10, 20) }), load });
      await waitFor(() => expect(load).toHaveBeenCalledTimes(3));
      expect(screen.getByTestId("task-thread-working-at").textContent).toBe("10:13");
      // New text: a new time.
      await r.rerender({ task: task({ progress: "Pulling request ids", updated_at: at(10, 20) }), load });
      await waitFor(() => expect(screen.getByTestId("task-thread-working-at").textContent).toBe("10:20"));
    } finally {
      now.mockRestore();
    }
  });

  test("progress ellipsis: ASCII ... goes, none stays intact, the cap's … stays", async () => {
    const load = vi.fn().mockResolvedValue(log);
    const shown = async (progress: string) => {
      const r = render(TaskThread, { props: { task: task({ progress }), load } });
      await waitFor(() => expect(screen.getByTestId("task-thread-progress")).toBeDefined());
      const text = screen.getByTestId("task-thread-progress").textContent;
      r.unmount();
      return text;
    };
    expect(await shown("Checking Loki ...")).toBe("Checking Loki");
    expect(await shown("Checking Loki for 401s")).toBe("Checking Loki for 401s");
    const long = "x".repeat(200);
    const cut = await shown(long);
    expect(cut).toBe("x".repeat(139) + "…");
    expect(Array.from(cut!)).toHaveLength(140);
  });

  /* P48 s19a (G1–G3). */
  test("userName shows initials and the name on the person's row", async () => {
    render(TaskThread, { props: { task: task({ state: "completed" }), load: vi.fn().mockResolvedValue([log[2]]), userName: "Yoga Setiawan" } });
    await waitFor(() => expect(screen.getAllByTestId("task-thread-msg")).toHaveLength(1));
    const you = screen.getAllByTestId("task-thread-msg")[0];
    expect(you.querySelector(".bg-green-600")!.textContent!.trim()).toBe("YS");
    expect(you.querySelector("b")!.textContent).toBe("Yoga Setiawan");
  });

  test("backticks render as code; HTML stays text", async () => {
    const load = vi.fn().mockResolvedValue([
      { role: "teammate", handle: "halodev", text: "see `app_id` now", at: at(10, 6) },
      { role: "teammate", handle: "halodev", text: "<img src=x onerror=alert(1)>", at: at(10, 7) },
    ]);
    render(TaskThread, { props: { task: task({ state: "completed" }), load } });
    await waitFor(() => expect(screen.getAllByTestId("task-thread-msg")).toHaveLength(2));
    const [code, html] = screen.getAllByTestId("task-thread-msg");
    expect(code.querySelector("code")!.textContent).toBe("app_id");
    expect(code.textContent).not.toContain("`");
    expect(html.querySelector("img")).toBeNull();
    expect(html.textContent).toContain("<img src=x onerror=alert(1)>");
  });

  test("the brief is skipped when it repeats the title, kept when it differs", async () => {
    const brief = (text: string, when = at(10, 0)): TeamThreadEntry[] => [
      { role: "captain", handle: "captain", text, at: when },
      { role: "teammate", handle: "halodev", text: "on it", at: at(10, 6) },
      { role: "captain", handle: "captain", text: "check the logs", at: at(10, 7) },
    ];
    const r = render(TaskThread, { props: { task: task({ state: "completed" }), load: vi.fn().mockResolvedValue(brief("  check the logs \n")) } });
    await waitFor(() => expect(screen.getAllByTestId("task-thread-msg")).toHaveLength(2));
    // Only the first row is the brief; a later captain row stays.
    expect(screen.getAllByTestId("task-thread-msg").map((m) => m.dataset.role)).toEqual(["teammate", "captain"]);
    r.unmount();
    render(TaskThread, { props: { task: task({ state: "completed" }), load: vi.fn().mockResolvedValue(brief("Check the webhook 401s")) } });
    await waitFor(() => expect(screen.getAllByTestId("task-thread-msg")).toHaveLength(3));
    expect(screen.getAllByTestId("task-thread-msg")[0].textContent).toContain("Check the webhook 401s");
  });

  test("a first Captain row logged after the start is not the brief", async () => {
    // The server keeps the latest entries only: entries[0] may be a later
    // Captain line that happens to repeat the title.
    const load = vi.fn().mockResolvedValue([
      { role: "captain", handle: "captain", text: "check the logs", at: at(10, 30) },
      { role: "teammate", handle: "halodev", text: "on it", at: at(10, 31) },
    ]);
    render(TaskThread, { props: { task: task({ state: "completed" }), load } });
    await waitFor(() => expect(screen.getAllByTestId("task-thread-msg")).toHaveLength(2));
    expect(screen.getAllByTestId("task-thread-msg")[0].dataset.role).toBe("captain");
  });

  test("empty userName: an icon and the You pill, no name", async () => {
    render(TaskThread, { props: { task: task({ state: "completed" }), load: vi.fn().mockResolvedValue([log[2]]), userName: "" } });
    await waitFor(() => expect(screen.getAllByTestId("task-thread-msg")).toHaveLength(1));
    const you = screen.getAllByTestId("task-thread-msg")[0];
    const avatar = you.querySelector(".bg-green-600")!;
    expect(avatar.querySelector("svg")).not.toBeNull();
    expect(avatar.textContent!.trim()).toBe("");
    expect(you.querySelector("b")).toBeNull();
    expect(screen.getByTestId("task-thread-you").textContent).toBe("You");
  });
});
