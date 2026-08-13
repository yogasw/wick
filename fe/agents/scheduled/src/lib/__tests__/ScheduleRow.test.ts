import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ScheduleRow from "../ScheduleRow.svelte";
import type { Schedule } from "../api.js";

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

const DONE: Schedule = {
  id: "sm_3",
  session_id: "s1",
  created_by: "ai",
  kind: "once",
  run_at: "2026-07-09T12:40:00Z",
  status: "done",
  message: "one shot",
  run_count: 1,
  session_mode: "existing",
};

/* A project job: no session_id at all — its target session is minted per fire.
   last_session_id is where the most recent run landed. */
const PROJECT_NEW: Schedule = {
  id: "sm_4",
  session_id: "",
  created_by: "user",
  kind: "recurring",
  run_at: "2026-08-17T09:00:00Z",
  status: "active",
  message: "write the weekly report",
  run_count: 1,
  cron: "0 9 * * 1",
  session_mode: "new",
  project_id: "p1",
  project_name: "Reports",
  last_session_id: "sch-abc-1",
  last_session_label: "Weekly report",
};

const PROJECT_TPL: Schedule = {
  id: "sm_5",
  session_id: "",
  created_by: "ai",
  kind: "recurring",
  run_at: "2026-08-14T09:00:00Z",
  status: "active",
  message: "daily digest",
  run_count: 0,
  interval_ms: 86400000,
  session_mode: "template",
  session_template: "daily-{date}",
  project_id: "p1",
  project_name: "Reports",
};

function cbs() {
  return { base: "/tools/agents", onCancel: vi.fn(), onPause: vi.fn(), onResume: vi.fn() };
}

describe("ScheduleRow", () => {
  test("recurring shows cadence + run count + by creator", () => {
    render(ScheduleRow, { props: { s: RECUR, ...cbs() } });
    expect(screen.getByText("every 5m")).toBeTruthy();
    expect(screen.getByText(/ran 3×/)).toBeTruthy();
    expect(screen.getByText("by user")).toBeTruthy();
  });

  test("active recurring: pause + cancel fire with id", async () => {
    const c = cbs();
    render(ScheduleRow, { props: { s: RECUR, ...c } });
    await fireEvent.click(screen.getByText("Pause"));
    expect(c.onPause).toHaveBeenCalledWith("sm_2");
    await fireEvent.click(screen.getByText("Cancel"));
    expect(c.onCancel).toHaveBeenCalledWith("sm_2");
  });

  test("paused shows Resume", async () => {
    const c = cbs();
    render(ScheduleRow, { props: { s: { ...RECUR, paused: true }, ...c } });
    await fireEvent.click(screen.getByText("Resume"));
    expect(c.onResume).toHaveBeenCalledWith("sm_2");
  });

  test("done row has no actions", () => {
    render(ScheduleRow, { props: { s: DONE, ...cbs() } });
    expect(screen.queryByText("Cancel")).toBeNull();
    expect(screen.queryByText("Pause")).toBeNull();
  });

  test("session-scoped row shows no scope badge", () => {
    render(ScheduleRow, { props: { s: RECUR, ...cbs() } });
    expect(screen.queryByTestId("scope-badge")).toBeNull();
  });

  test("project-scoped 'new' row labels the per-run session", () => {
    render(ScheduleRow, { props: { s: PROJECT_NEW, ...cbs() } });
    expect(screen.getByTestId("scope-badge").textContent).toContain("new session each run");
  });

  test("project-scoped 'template' row shows the pattern", () => {
    render(ScheduleRow, { props: { s: PROJECT_TPL, ...cbs() } });
    expect(screen.getByTestId("scope-badge").textContent).toContain("daily-{date}");
  });

  test("last run links to the session the fire landed in", () => {
    render(ScheduleRow, { props: { s: PROJECT_NEW, ...cbs() } });
    const link = screen.getByTestId("last-run-link") as HTMLAnchorElement;
    expect(link.textContent).toContain("Weekly report");
    expect(link.getAttribute("href")).toBe("/tools/agents/sessions/sch-abc-1");
  });

  test("a project job that never fired shows no last-run link", () => {
    render(ScheduleRow, { props: { s: PROJECT_TPL, ...cbs() } });
    expect(screen.queryByTestId("last-run-link")).toBeNull();
  });

  test("clicking the row opens the detail modal", async () => {
    const onOpen = vi.fn();
    render(ScheduleRow, { props: { s: PROJECT_NEW, ...cbs(), onOpen } });
    await fireEvent.click(screen.getByTestId("row-open"));
    expect(onOpen).toHaveBeenCalledWith(PROJECT_NEW);
  });

  test("without onOpen the row is inert (no button, no handler)", () => {
    render(ScheduleRow, { props: { s: PROJECT_NEW, ...cbs() } });
    expect(screen.queryByTestId("row-open")).toBeNull();
  });

  test("Run now fires the schedule without touching its definition", async () => {
    const onRunNow = vi.fn();
    render(ScheduleRow, { props: { s: PROJECT_NEW, ...cbs(), onRunNow } });
    await fireEvent.click(screen.getByTestId("run-now"));
    expect(onRunNow).toHaveBeenCalledWith("sm_4");
  });

  test("a done row offers no actions, Run now included", () => {
    render(ScheduleRow, { props: { s: DONE, ...cbs(), onRunNow: vi.fn() } });
    expect(screen.queryByTestId("run-now")).toBeNull();
    expect(screen.queryByText("Cancel")).toBeNull();
  });

  test("a terminal row with no next fire falls back to its last run", () => {
    // The backend stops publishing run_at once a schedule is done (it would
    // otherwise be the ~100-year claim sentinel), so the row must not render
    // an empty date.
    const finished: Schedule = {
      ...DONE,
      run_at: "",
      last_run_at: "2026-08-13T11:19:36Z",
    };
    render(ScheduleRow, { props: { s: finished, ...cbs() } });
    expect(screen.queryByText("—")).toBeNull();
    // Compare against the same locale formatting the component uses, so the
    // assertion doesn't depend on the runner's timezone.
    const expected = new Date(finished.last_run_at!).toLocaleString(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
    expect(screen.getAllByText(expected).length).toBeGreaterThan(0);
  });

  test("a paused row keeps its actions — 'paused' is a live status", async () => {
    // The API reports status="paused" for a live-but-suspended schedule. A
    // bare pending/active check would treat it as terminal and strip Resume,
    // leaving no way to un-pause it from the UI.
    const c = cbs();
    render(ScheduleRow, {
      props: { s: { ...RECUR, status: "paused", paused: true }, ...c },
    });
    await fireEvent.click(screen.getByText("Resume"));
    expect(c.onResume).toHaveBeenCalledWith("sm_2");
    expect(screen.getByText("Cancel")).toBeTruthy();
  });

  test("a paused row shows no 'next' time (it isn't going to fire)", () => {
    render(ScheduleRow, {
      props: { s: { ...RECUR, status: "paused", paused: true }, ...cbs() },
    });
    expect(screen.queryByText(/next /)).toBeNull();
  });

  test("a terminal row with neither time shows a dash, not 'Invalid Date'", () => {
    const finished: Schedule = { ...DONE, run_at: "", last_run_at: undefined };
    render(ScheduleRow, { props: { s: finished, ...cbs() } });
    expect(screen.getByText("—")).toBeTruthy();
  });
});
