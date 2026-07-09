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

  test("renders once + recurring with cadence and status", () => {
    render(SchedulePanel, { props: { schedules: [ONCE, RECUR, DONE], ...cbs() } });
    expect(screen.getByText("check the deploy")).toBeTruthy();
    expect(screen.getByText("poll loki")).toBeTruthy();
    expect(screen.getByText("every 5m")).toBeTruthy(); // 300000ms → 5m
    expect(screen.getByText("done")).toBeTruthy();
    // recurring meta line shows run count
    expect(screen.getByText(/ran 3×/)).toBeTruthy();
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
});
