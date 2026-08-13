import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import SchedulePanel from "../SchedulePanel.svelte";
import type { Schedule } from "../../types/agents.js";

const ONCE: Schedule = {
  id: "sm_1",
  session_id: "s1",
  created_by: "ai",
  kind: "once",
  run_at: "2026-07-09T12:40:00Z",
  status: "pending",
  message: "check the deploy",
  run_count: 0,
  session_mode: "existing",
};

const RECUR: Schedule = {
  id: "sm_2",
  session_id: "s1",
  created_by: "user",
  kind: "recurring",
  run_at: "2026-07-09T12:45:00Z",
  status: "active",
  message: "poll loki",
  run_count: 3,
  interval_ms: 300000,
  session_mode: "existing",
};

const DONE: Schedule = { ...ONCE, id: "sm_3", status: "done", message: "already ran" };

function cbs() {
  return {
    onCreate: vi.fn().mockResolvedValue(true),
    onCancel: vi.fn(),
    onPause: vi.fn(),
    onResume: vi.fn(),
  };
}

describe("SchedulePanel", () => {
  test("empty state", () => {
    render(SchedulePanel, { props: { schedules: [], ...cbs() } });
    expect(screen.getByText("No scheduled messages.")).toBeTruthy();
  });

  test("renders once + recurring with cadence and status", async () => {
    render(SchedulePanel, { props: { schedules: [ONCE, RECUR, DONE], ...cbs() } });
    expect(screen.getByText("check the deploy")).toBeTruthy();
    expect(screen.getByText("poll loki")).toBeTruthy();
    expect(screen.getByText("every 5m")).toBeTruthy(); // 300000ms → 5m
    // recurring meta line shows run count
    expect(screen.getByText(/ran 3×/)).toBeTruthy();
    // The done row is filtered out of the default Live view; it shows with
    // its status badge under Finished.
    expect(screen.queryByText("already ran")).toBeNull();
    await fireEvent.click(screen.getByTestId("list-filter-done"));
    expect(screen.getByText("done")).toBeTruthy();
    expect(screen.getByText("already ran")).toBeTruthy();
  });

  test("one-shot create uses selected preset", async () => {
    const c = cbs();
    render(SchedulePanel, { props: { schedules: [], ...c } });
    await fireEvent.input(screen.getByTestId("sched-message"), { target: { value: "look again" } });
    await fireEvent.click(screen.getByText("Schedule"));
    expect(c.onCreate).toHaveBeenCalledWith({ message: "look again", runAt: "1h" });
  });

  test("repeat mode with interval + max runs", async () => {
    const c = cbs();
    render(SchedulePanel, { props: { schedules: [], ...c } });
    await fireEvent.click(screen.getByTestId("mode-repeat"));
    // default preset is 5m
    await fireEvent.input(screen.getByTestId("repeat-maxruns"), { target: { value: "10" } });
    await fireEvent.input(screen.getByTestId("sched-message"), { target: { value: "cek loki" } });
    await fireEvent.click(screen.getByText("Schedule"));
    expect(c.onCreate).toHaveBeenCalledWith({ message: "cek loki", every: "5m", maxRuns: 10 });
  });

  test("cron mode feeds cron arg", async () => {
    const c = cbs();
    render(SchedulePanel, { props: { schedules: [], ...c } });
    await fireEvent.click(screen.getByTestId("mode-repeat"));
    await fireEvent.change(screen.getByTestId("repeat-when"), { target: { value: "cron" } });
    await fireEvent.input(screen.getByTestId("repeat-cron"), { target: { value: "0 9 * * 1" } });
    await fireEvent.input(screen.getByTestId("sched-message"), { target: { value: "weekly" } });
    await fireEvent.click(screen.getByText("Schedule"));
    expect(c.onCreate).toHaveBeenCalledWith({ message: "weekly", cron: "0 9 * * 1", maxRuns: undefined });
  });

  test("pause fires for active recurring; cancel for any live row", async () => {
    const c = cbs();
    render(SchedulePanel, { props: { schedules: [RECUR], ...c } });
    await fireEvent.click(screen.getByText("Pause"));
    expect(c.onPause).toHaveBeenCalledWith("sm_2");
    await fireEvent.click(screen.getByText("Cancel"));
    expect(c.onCancel).toHaveBeenCalledWith("sm_2");
  });

  test("paused recurring shows Resume", async () => {
    const c = cbs();
    render(SchedulePanel, { props: { schedules: [{ ...RECUR, paused: true }], ...c } });
    await fireEvent.click(screen.getByText("Resume"));
    expect(c.onResume).toHaveBeenCalledWith("sm_2");
  });

  test("done rows have no actions", () => {
    render(SchedulePanel, { props: { schedules: [DONE], ...cbs() } });
    expect(screen.queryByText("Cancel")).toBeNull();
    expect(screen.queryByText("Pause")).toBeNull();
  });

  /* ── target scope ──────────────────────────────────────────────────── */

  const PROJECTS = [
    { id: "p1", name: "Reports" },
    { id: "p2", name: "Infra" },
  ];

  test("no projects available: the target selector is hidden entirely", () => {
    // With nowhere to run a project job, offering the choice is a dead end.
    render(SchedulePanel, { props: { schedules: [], ...cbs() } });
    expect(screen.queryByTestId("target-existing")).toBeNull();
    expect(screen.queryByTestId("target-new")).toBeNull();
  });

  test("default target stays this session, sending no scope fields", async () => {
    const c = cbs();
    render(SchedulePanel, { props: { schedules: [], projects: PROJECTS, ...c } });
    await fireEvent.input(screen.getByTestId("sched-message"), { target: { value: "ping" } });
    await fireEvent.click(screen.getByText("Schedule"));
    expect(c.onCreate).toHaveBeenCalledWith({ message: "ping", runAt: "1h" });
  });

  test("picking 'new session each run' pre-selects this session's project", async () => {
    const c = cbs();
    render(SchedulePanel, {
      props: { schedules: [], projects: PROJECTS, currentProjectId: "p2", ...c },
    });
    await fireEvent.click(screen.getByTestId("target-new"));
    await fireEvent.input(screen.getByTestId("sched-message"), { target: { value: "report" } });
    await fireEvent.click(screen.getByText("Schedule"));
    expect(c.onCreate).toHaveBeenCalledWith({
      message: "report",
      runAt: "1h",
      projectId: "p2",
      sessionMode: "new",
      sessionTemplate: undefined,
    });
  });

  test("a project target with no project selected is refused", async () => {
    const c = cbs();
    render(SchedulePanel, { props: { schedules: [], projects: PROJECTS, ...c } });
    await fireEvent.click(screen.getByTestId("target-new"));
    await fireEvent.input(screen.getByTestId("sched-message"), { target: { value: "x" } });
    await fireEvent.click(screen.getByText("Schedule"));
    expect(c.onCreate).not.toHaveBeenCalled();
    expect(screen.getByTestId("sched-error").textContent).toContain("Pick a project");
  });

  test("template target requires a pattern, and says so", async () => {
    const c = cbs();
    render(SchedulePanel, {
      props: { schedules: [], projects: PROJECTS, currentProjectId: "p1", ...c },
    });
    await fireEvent.click(screen.getByTestId("target-template"));
    await fireEvent.input(screen.getByTestId("sched-message"), { target: { value: "x" } });
    await fireEvent.click(screen.getByText("Schedule"));
    expect(c.onCreate).not.toHaveBeenCalled();
    expect(screen.getByTestId("sched-error").textContent).toContain("session name pattern");
  });

  test("template target sends the pattern and previews the first session", async () => {
    const c = cbs();
    render(SchedulePanel, {
      props: { schedules: [], projects: PROJECTS, currentProjectId: "p1", ...c },
    });
    await fireEvent.click(screen.getByTestId("target-template"));
    await fireEvent.input(screen.getByTestId("target-template-pattern"), { target: { value: "daily-{date}" } });

    const now = new Date();
    const iso = `${now.getUTCFullYear()}-${String(now.getUTCMonth() + 1).padStart(2, "0")}-${String(now.getUTCDate()).padStart(2, "0")}`;
    expect(screen.getByTestId("target-preview").textContent).toContain(`daily-${iso}`);

    await fireEvent.input(screen.getByTestId("sched-message"), { target: { value: "digest" } });
    await fireEvent.click(screen.getByText("Schedule"));
    expect(c.onCreate).toHaveBeenCalledWith({
      message: "digest",
      runAt: "1h",
      projectId: "p1",
      sessionMode: "template",
      sessionTemplate: "daily-{date}",
    });
  });

  test("a recurring project job carries both cadence and scope", async () => {
    const c = cbs();
    render(SchedulePanel, {
      props: { schedules: [], projects: PROJECTS, currentProjectId: "p1", ...c },
    });
    await fireEvent.click(screen.getByTestId("mode-repeat"));
    await fireEvent.click(screen.getByTestId("target-new"));
    await fireEvent.input(screen.getByTestId("sched-message"), { target: { value: "poll" } });
    await fireEvent.click(screen.getByText("Schedule"));
    expect(c.onCreate).toHaveBeenCalledWith({
      message: "poll",
      every: "5m",
      maxRuns: undefined,
      projectId: "p1",
      sessionMode: "new",
      sessionTemplate: undefined,
    });
  });

  test("a listed project job says where it delivers instead of here", () => {
    const job: Schedule = {
      ...RECUR,
      id: "sm_9",
      session_id: "",
      session_mode: "new",
      project_id: "p1",
      project_name: "Reports",
    };
    render(SchedulePanel, { props: { schedules: [job], ...cbs() } });
    expect(screen.getByTestId("row-scope").textContent).toContain("new session each run");
  });

  test("a listed template job shows its pattern", () => {
    const job: Schedule = {
      ...RECUR,
      id: "sm_10",
      session_id: "",
      session_mode: "template",
      session_template: "daily-{date}",
      project_id: "p1",
    };
    render(SchedulePanel, { props: { schedules: [job], ...cbs() } });
    expect(screen.getByTestId("row-scope").textContent).toContain("daily-{date}");
  });

  test("a session-scoped row shows no scope badge", () => {
    render(SchedulePanel, { props: { schedules: [RECUR], ...cbs() } });
    expect(screen.queryByTestId("row-scope")).toBeNull();
  });

  /* ── status filter ─────────────────────────────────────────────────── */

  const CANCELLED: Schedule = { ...ONCE, id: "sm_c", status: "cancelled", message: "gave up" };
  const FAILED: Schedule = { ...ONCE, id: "sm_f", status: "failed", message: "blew up" };

  test("defaults to live, keeping finished rows out of the way", () => {
    // The regression this guards: a session that has run a few schedules
    // buried the live ones under every done/cancelled row.
    render(SchedulePanel, { props: { schedules: [RECUR, DONE, CANCELLED], ...cbs() } });
    expect(screen.getByText("poll loki")).toBeTruthy();
    expect(screen.queryByText("already ran")).toBeNull();
    expect(screen.queryByText("gave up")).toBeNull();
  });

  test("the Finished tab shows done, cancelled and failed", async () => {
    render(SchedulePanel, { props: { schedules: [RECUR, DONE, CANCELLED, FAILED], ...cbs() } });
    await fireEvent.click(screen.getByTestId("list-filter-done"));
    expect(screen.getByText("already ran")).toBeTruthy();
    expect(screen.getByText("gave up")).toBeTruthy();
    expect(screen.getByText("blew up")).toBeTruthy();
    expect(screen.queryByText("poll loki")).toBeNull();
  });

  test("the All tab shows everything", async () => {
    render(SchedulePanel, { props: { schedules: [RECUR, DONE], ...cbs() } });
    await fireEvent.click(screen.getByTestId("list-filter-all"));
    expect(screen.getByText("poll loki")).toBeTruthy();
    expect(screen.getByText("already ran")).toBeTruthy();
  });

  test("tab counts report the full set, not the filtered view", () => {
    render(SchedulePanel, { props: { schedules: [RECUR, DONE, CANCELLED, FAILED], ...cbs() } });
    expect(screen.getByTestId("list-filter-live").textContent).toContain("1");
    expect(screen.getByTestId("list-filter-done").textContent).toContain("3");
    expect(screen.getByTestId("list-filter-all").textContent).toContain("4");
  });

  test("an all-finished session says so instead of looking empty", () => {
    render(SchedulePanel, { props: { schedules: [DONE], ...cbs() } });
    // Live is still the default, so the list is empty — but there IS history,
    // and the tab counts plus this message have to make that legible.
    expect(screen.getByTestId("list-empty").textContent).toContain("Nothing scheduled right now");
    expect(screen.getByTestId("list-filter-done").textContent).toContain("1");
  });

  test("a paused row counts as Live and keeps its actions", async () => {
    // status="paused" is reported for a live-but-suspended schedule; filtering
    // it into Finished would hide the only way to resume it.
    const c = cbs();
    const paused: Schedule = { ...RECUR, status: "paused", paused: true };
    render(SchedulePanel, { props: { schedules: [paused], ...c } });
    expect(screen.getByText("poll loki")).toBeTruthy();
    expect(screen.getByTestId("list-filter-live").textContent).toContain("1");
    await fireEvent.click(screen.getByText("Resume"));
    expect(c.onResume).toHaveBeenCalledWith("sm_2");
  });

  test("no schedules at all: no tabs, just the plain empty state", () => {
    render(SchedulePanel, { props: { schedules: [], ...cbs() } });
    expect(screen.getByText("No scheduled messages.")).toBeTruthy();
    expect(screen.queryByTestId("list-filter-live")).toBeNull();
  });

  /* ── collapsible create form ───────────────────────────────────────── */

  test("the create form is collapsed when there is already a list", () => {
    render(SchedulePanel, { props: { schedules: [RECUR], ...cbs() } });
    expect(screen.queryByTestId("sched-message")).toBeNull();
    expect(screen.getByTestId("toggle-composer")).toBeTruthy();
  });

  test("the create form is open on an empty panel, where it IS the content", () => {
    render(SchedulePanel, { props: { schedules: [], ...cbs() } });
    expect(screen.getByTestId("sched-message")).toBeTruthy();
  });

  test("toggling reveals the form and it still schedules", async () => {
    const c = cbs();
    render(SchedulePanel, { props: { schedules: [RECUR], ...c } });
    await fireEvent.click(screen.getByTestId("toggle-composer"));
    await fireEvent.input(screen.getByTestId("sched-message"), { target: { value: "later" } });
    await fireEvent.click(screen.getByText("Schedule"));
    expect(c.onCreate).toHaveBeenCalledWith({ message: "later", runAt: "1h" });
  });

  /* ── row click ─────────────────────────────────────────────────────── */

  test("clicking a row opens the detail modal", async () => {
    render(SchedulePanel, { props: { schedules: [RECUR], ...cbs(), onReschedule: vi.fn() } });
    await fireEvent.click(screen.getByTestId("row-open"));
    expect(screen.getByTestId("schedule-edit")).toBeTruthy();
  });

  test("rows are inert without onReschedule", () => {
    render(SchedulePanel, { props: { schedules: [RECUR], ...cbs() } });
    expect(screen.queryByTestId("row-open")).toBeNull();
  });

  test("Run now fires the schedule", async () => {
    const onRunNow = vi.fn();
    render(SchedulePanel, { props: { schedules: [RECUR], ...cbs(), onRunNow } });
    await fireEvent.click(screen.getByTestId("run-now"));
    expect(onRunNow).toHaveBeenCalledWith("sm_2");
  });
});
