import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import JobDetail from "../JobDetail.svelte";
import * as api from "$lib/api.js";
import type { JobDetail as JobDetailType } from "$lib/types.js";

vi.mock("$lib/api.js");

function makeJob(over: Partial<JobDetailType> = {}): JobDetailType {
  return {
    key: "report",
    name: "Daily Report",
    description: "Sends a daily report.",
    icon: "📊",
    schedule: "0 9 * * *",
    enabled: true,
    max_runs: 0,
    max_timeout_min: 30,
    total_runs: 3,
    last_status: "idle",
    can_configure: true,
    fields: [{ key: "endpoint", type: "url", value: "", options: "", required: true, is_secret: false, has_value: false, description: "", visible_when: "", env_override: "" }],
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getJob).mockResolvedValue(makeJob());
});

afterEach(() => {
  vi.useRealTimers();
});

describe("JobDetail", () => {
  it("renders the job header + prefilled schedule", async () => {
    render(JobDetail, { jobKey: "report" });
    expect(await screen.findByText("Daily Report")).toBeTruthy();
    expect((screen.getByLabelText("Cron expression") as HTMLInputElement).value).toBe("0 9 * * *");
  });

  it("saves settings through updateJobSettings", async () => {
    vi.mocked(api.updateJobSettings).mockResolvedValue(undefined);
    render(JobDetail, { jobKey: "report" });
    await screen.findByText("Daily Report");
    await fireEvent.input(screen.getByLabelText("Cron expression"), { target: { value: "*/5 * * * *" } });
    await fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.updateJobSettings).toHaveBeenCalled());
    expect(api.updateJobSettings).toHaveBeenCalledWith("report", {
      schedule: "*/5 * * * *",
      enabled: true,
      max_runs: 0,
      max_timeout_min: 30,
    });
  });

  it("hides the Save button when not configurable", async () => {
    vi.mocked(api.getJob).mockResolvedValue(makeJob({ can_configure: false }));
    render(JobDetail, { jobKey: "report" });
    await screen.findByText("Daily Report");
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
  });

  it("runs the job and polls the run to completion", async () => {
    vi.useFakeTimers();
    vi.mocked(api.runJob).mockResolvedValue("run-1");
    vi.mocked(api.getJobRun)
      .mockResolvedValueOnce({ id: "run-1", job_id: "j", status: "running", result: "", triggered_by: "manual", started_at: "", ended_at: null })
      .mockResolvedValueOnce({ id: "run-1", job_id: "j", status: "success", result: "# all good", triggered_by: "manual", started_at: "", ended_at: null });

    render(JobDetail, { jobKey: "report" });
    await vi.waitFor(() => expect(screen.getByText("Daily Report")).toBeTruthy());

    await fireEvent.click(screen.getByRole("button", { name: "Run Now" }));
    await vi.waitFor(() => expect(api.runJob).toHaveBeenCalledWith("report"));

    /* First poll tick → still running; second → success. */
    await vi.advanceTimersByTimeAsync(1500);
    await vi.advanceTimersByTimeAsync(1500);

    await vi.waitFor(() => expect(api.getJobRun).toHaveBeenCalledTimes(2));
    expect(screen.getByText("all good")).toBeTruthy();
  });
});
