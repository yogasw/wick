import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ScheduleEditModal from "../ScheduleEditModal.svelte";
import type { EditableSchedule } from "../schedule-edit-types.js";
import {
  isLegalSessionID,
  isProjectScopedSchedule,
  renderSessionTemplate,
  scheduleCadence,
} from "../schedule-edit-types.js";

const NUDGE: EditableSchedule = {
  id: "sm_1",
  kind: "recurring",
  run_at: "2026-08-17T09:00:00Z",
  status: "active",
  message: "poll loki",
  run_count: 3,
  created_by: "user",
  interval_ms: 300000,
  session_id: "s1",
  session_mode: "existing",
};

const JOB: EditableSchedule = {
  id: "sm_2",
  kind: "recurring",
  run_at: "2026-08-17T09:00:00Z",
  status: "active",
  message: "write the weekly report",
  run_count: 2,
  created_by: "ai",
  cron: "0 9 * * 1",
  session_mode: "new",
  project_id: "p1",
  project_name: "Reports",
  last_session_id: "sch-abc-2",
  last_session_label: "Weekly report",
};

const TPL: EditableSchedule = {
  ...JOB,
  id: "sm_3",
  session_mode: "template",
  session_template: "daily-{date}",
  last_session_id: undefined,
  last_session_label: undefined,
};

const PROJECTS = [
  { id: "p1", name: "Reports" },
  { id: "p2", name: "Infra" },
];

function props(schedule: EditableSchedule, extra: Record<string, unknown> = {}) {
  return {
    open: true,
    schedule,
    projects: PROJECTS,
    onSave: vi.fn(),
    onClose: vi.fn(),
    ...extra,
  };
}

describe("ScheduleEditModal — detail view", () => {
  test("shows the facts a click on the row is asking for", () => {
    render(ScheduleEditModal, { props: props(JOB) });
    expect(screen.getByText("cron 0 9 * * 1")).toBeTruthy();
    expect(screen.getByText("ai")).toBeTruthy();
    expect(screen.getByText("sm_2")).toBeTruthy();
    expect(screen.getByTestId("detail-target").textContent).toContain("new session each run");
    expect(screen.getByTestId("detail-target").textContent).toContain("Reports");
  });

  test("a session nudge says it delivers here, with no scope controls", () => {
    render(ScheduleEditModal, { props: props(NUDGE) });
    expect(screen.getByTestId("detail-target").textContent).toContain("this session");
    // A session-scoped schedule has no target config to edit.
    expect(screen.queryByTestId("edit-project")).toBeNull();
    expect(screen.queryByTestId("edit-mode-new")).toBeNull();
  });

  test("links the session the last run landed in", () => {
    render(ScheduleEditModal, {
      props: props(JOB, { sessionHref: (id: string) => `/s/${id}` }),
    });
    const link = screen.getByTestId("detail-last-run") as HTMLAnchorElement;
    expect(link.getAttribute("href")).toBe("/s/sch-abc-2");
  });

  test("a job that never fired shows no last-run link", () => {
    render(ScheduleEditModal, {
      props: props(TPL, { sessionHref: (id: string) => `/s/${id}` }),
    });
    expect(screen.queryByTestId("detail-last-run")).toBeNull();
  });

  test("surfaces the last delivery error", () => {
    render(ScheduleEditModal, { props: props({ ...JOB, last_error: "pool boom" }) });
    expect(screen.getByTestId("detail-error").textContent).toContain("pool boom");
  });

  test("names the timezone a cron was read in", () => {
    // "9am" in the wrong zone is hours off, and the expression alone doesn't
    // say which zone it means — so the row has to.
    render(ScheduleEditModal, {
      props: props({ ...JOB, cron_timezone: "Asia/Jakarta (UTC+07:00)" }),
    });
    expect(screen.getByTestId("detail-timezone").textContent).toContain("Asia/Jakarta");
  });

  test("an interval schedule shows no timezone (it has no wall-clock meaning)", () => {
    render(ScheduleEditModal, { props: props({ ...NUDGE, cron_timezone: "Asia/Jakarta" }) });
    expect(screen.queryByTestId("detail-timezone")).toBeNull();
  });

  test("manual runs are counted apart from scheduled ones", () => {
    // Only scheduled fires count against max_runs, so mixing them into one
    // number would misreport how much budget is left.
    render(ScheduleEditModal, { props: props({ ...JOB, run_count: 2, manual_runs: 3 }) });
    expect(screen.getByTestId("detail-manual-runs").textContent).toContain("3");
  });

  test("no manual runs: no extra clutter", () => {
    render(ScheduleEditModal, { props: props(JOB) });
    expect(screen.queryByTestId("detail-manual-runs")).toBeNull();
  });

  test("a paused schedule is still editable — 'paused' is a live status", () => {
    render(ScheduleEditModal, { props: props({ ...JOB, status: "paused", paused: true }) });
    expect(screen.getByTestId("edit-save")).toBeTruthy();
    expect(screen.getByTestId("edit-timing")).toBeTruthy();
  });

  test("a finished schedule is read-only — nothing to save", () => {
    render(ScheduleEditModal, { props: props({ ...JOB, status: "done" }) });
    expect(screen.queryByTestId("edit-save")).toBeNull();
    expect(screen.queryByTestId("edit-timing")).toBeNull();
    expect(screen.getByTestId("edit-cancel").textContent).toContain("Close");
  });
});

describe("ScheduleEditModal — editing", () => {
  test("seeds the timing field with the current cadence", () => {
    render(ScheduleEditModal, { props: props(NUDGE) });
    // 300000ms → "5m", so leaving it alone is a no-op rather than a reset.
    expect((screen.getByTestId("edit-timing") as HTMLInputElement).value).toBe("5m");
  });

  test("a cron schedule seeds the cron expression", () => {
    render(ScheduleEditModal, { props: props(JOB) });
    expect((screen.getByTestId("edit-timing") as HTMLInputElement).value).toBe("0 9 * * 1");
  });

  test("changing nothing is refused instead of sending an empty patch", async () => {
    const p = props(NUDGE);
    render(ScheduleEditModal, { props: p });
    await fireEvent.click(screen.getByTestId("edit-save"));
    expect(p.onSave).not.toHaveBeenCalled();
    expect(screen.getByTestId("edit-error").textContent).toContain("Nothing changed");
  });

  test("editing the interval sends it as `every`", async () => {
    const p = props(NUDGE);
    render(ScheduleEditModal, { props: p });
    await fireEvent.input(screen.getByTestId("edit-timing"), { target: { value: "10m" } });
    await fireEvent.click(screen.getByTestId("edit-save"));
    expect(p.onSave).toHaveBeenCalledWith(expect.objectContaining({ every: "10m" }));
  });

  test("editing the message sends only the message", async () => {
    const p = props(NUDGE);
    render(ScheduleEditModal, { props: p });
    await fireEvent.input(screen.getByTestId("edit-message"), { target: { value: "poll harder" } });
    await fireEvent.click(screen.getByTestId("edit-save"));
    expect(p.onSave).toHaveBeenCalledWith({ message: "poll harder" });
  });

  test("a one-shot cannot be turned into a recurring one", () => {
    render(ScheduleEditModal, { props: props({ ...NUDGE, kind: "once", interval_ms: undefined }) });
    // The server refuses the kind flip, so the UI never offers it.
    expect(screen.getByTestId("edit-kind-run_at")).toBeTruthy();
    expect(screen.queryByTestId("edit-kind-every")).toBeNull();
    expect(screen.queryByTestId("edit-kind-cron")).toBeNull();
  });

  test("a recurring schedule cannot be turned into a one-shot", () => {
    render(ScheduleEditModal, { props: props(NUDGE) });
    expect(screen.queryByTestId("edit-kind-run_at")).toBeNull();
    expect(screen.getByTestId("edit-kind-every")).toBeTruthy();
    expect(screen.getByTestId("edit-kind-cron")).toBeTruthy();
  });

  test("max runs must be a number", async () => {
    const p = props(NUDGE);
    render(ScheduleEditModal, { props: p });
    await fireEvent.input(screen.getByTestId("edit-maxruns"), { target: { value: "abc" } });
    await fireEvent.click(screen.getByTestId("edit-save"));
    expect(p.onSave).not.toHaveBeenCalled();
    expect(screen.getByTestId("edit-error").textContent).toContain("positive number");
  });

  test("switching a job to a named session sends the pattern", async () => {
    const p = props(JOB);
    render(ScheduleEditModal, { props: p });
    await fireEvent.click(screen.getByTestId("edit-mode-template"));
    await fireEvent.input(screen.getByTestId("edit-template"), { target: { value: "daily-{date}" } });
    await fireEvent.click(screen.getByTestId("edit-save"));
    expect(p.onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        project_id: "p1",
        session_mode: "template",
        session_template: "daily-{date}",
      }),
    );
  });

  test("switching back to per-run clears a stale pattern", async () => {
    const p = props(TPL);
    render(ScheduleEditModal, { props: p });
    await fireEvent.click(screen.getByTestId("edit-mode-new"));
    await fireEvent.click(screen.getByTestId("edit-save"));
    expect(p.onSave).toHaveBeenCalledWith(
      expect.objectContaining({ session_mode: "new", session_template: "" }),
    );
  });

  test("a pattern that renders an illegal session name is refused", async () => {
    const p = props(TPL);
    render(ScheduleEditModal, { props: p });
    await fireEvent.input(screen.getByTestId("edit-template"), { target: { value: "bad/{date}" } });
    await fireEvent.click(screen.getByTestId("edit-save"));
    expect(p.onSave).not.toHaveBeenCalled();
    expect(screen.getByTestId("edit-error").textContent).toContain("invalid session name");
  });

  test("an empty pattern is refused", async () => {
    const p = props(TPL);
    render(ScheduleEditModal, { props: p });
    await fireEvent.input(screen.getByTestId("edit-template"), { target: { value: "" } });
    await fireEvent.click(screen.getByTestId("edit-save"));
    expect(p.onSave).not.toHaveBeenCalled();
    expect(screen.getByTestId("edit-error").textContent).toContain("session name pattern");
  });

  test("previews the session the next run will land in", () => {
    render(ScheduleEditModal, { props: props(TPL) });
    const now = new Date();
    const iso = `${now.getUTCFullYear()}-${String(now.getUTCMonth() + 1).padStart(2, "0")}-${String(now.getUTCDate()).padStart(2, "0")}`;
    expect(screen.getByTestId("edit-preview").textContent).toContain(`daily-${iso}`);
  });

  test("warns before moving a job out of the project being viewed", async () => {
    const p = props(JOB);
    render(ScheduleEditModal, { props: p });
    expect(screen.queryByTestId("edit-move-warning")).toBeNull();
    await fireEvent.change(screen.getByTestId("edit-project"), { target: { value: "p2" } });
    // Moving projects makes the job vanish from this list, so say so first.
    expect(screen.getByTestId("edit-move-warning")).toBeTruthy();
  });

  test("keeps the current project selectable when it is not in the options", () => {
    // Renamed project, or access changed: saving another field must not
    // silently repoint the job somewhere else.
    render(ScheduleEditModal, { props: props(JOB, { projects: [{ id: "p9", name: "Other" }] }) });
    const select = screen.getByTestId("edit-project") as HTMLSelectElement;
    expect([...select.options].map((o) => o.value)).toContain("p1");
  });

  test("saving is disabled while a save is in flight", () => {
    render(ScheduleEditModal, { props: props(JOB, { saving: true }) });
    expect((screen.getByTestId("edit-save") as HTMLButtonElement).disabled).toBe(true);
  });

  test("cancel closes without saving", async () => {
    const p = props(JOB);
    render(ScheduleEditModal, { props: p });
    await fireEvent.click(screen.getByTestId("edit-cancel"));
    expect(p.onClose).toHaveBeenCalled();
    expect(p.onSave).not.toHaveBeenCalled();
  });
});

describe("schedule-edit helpers", () => {
  test("scheduleCadence renders intervals in the largest whole unit", () => {
    expect(scheduleCadence({ interval_ms: 300000 })).toBe("every 5m");
    expect(scheduleCadence({ interval_ms: 3600000 })).toBe("every 1h");
    expect(scheduleCadence({ interval_ms: 86400000 })).toBe("every 1d");
    expect(scheduleCadence({ interval_ms: 30000 })).toBe("every 30s");
    expect(scheduleCadence({ interval_ms: 90000 })).toBe("every 90s");
    expect(scheduleCadence({ interval_ms: 5400000 })).toBe("every 90m");
    expect(scheduleCadence({ cron: "0 9 * * 1" })).toBe("cron 0 9 * * 1");
    expect(scheduleCadence({})).toBe("recurring");
  });

  test("isProjectScopedSchedule treats a missing mode as session-scoped", () => {
    // Rows written before project scope existed carry no session_mode.
    expect(isProjectScopedSchedule({})).toBe(false);
    expect(isProjectScopedSchedule({ session_mode: "existing" })).toBe(false);
    expect(isProjectScopedSchedule({ session_mode: "new" })).toBe(true);
    expect(isProjectScopedSchedule({ session_mode: "template" })).toBe(true);
  });

  test("renderSessionTemplate mirrors the server's placeholders in UTC", () => {
    const at = new Date("2026-08-13T09:05:00Z");
    expect(renderSessionTemplate("r-{date}", 1, at)).toBe("r-2026-08-13");
    expect(renderSessionTemplate("r-{datetime}", 1, at)).toBe("r-2026-08-13-0905");
    expect(renderSessionTemplate("r-{ym}", 1, at)).toBe("r-2026-08");
    expect(renderSessionTemplate("r-{run}", 7, at)).toBe("r-7");
    expect(renderSessionTemplate("nightly", 1, at)).toBe("nightly");
  });

  test("isLegalSessionID matches the server's charset", () => {
    expect(isLegalSessionID("daily-2026-08-13")).toBe(true);
    expect(isLegalSessionID("a.b_c-1")).toBe(true);
    expect(isLegalSessionID("bad/slash")).toBe(false);
    expect(isLegalSessionID("has space")).toBe(false);
    expect(isLegalSessionID("")).toBe(false);
  });
});
