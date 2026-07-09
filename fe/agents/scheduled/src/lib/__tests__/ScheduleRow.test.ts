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
};

function cbs() {
  return { onCancel: vi.fn(), onPause: vi.fn(), onResume: vi.fn() };
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
});
