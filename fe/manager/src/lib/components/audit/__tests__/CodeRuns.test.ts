import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import CodeRuns from "../CodeRuns.svelte";
import * as api from "$lib/api.js";
import type { CodeRunResult } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("@wick-fe/common-stores", () => ({ toastError: vi.fn() }));

function makeResult(): CodeRunResult {
  return {
    runs: [
      {
        id: "r1", user_id: "u-1", user_name: "Alice", session_id: "s-1", project_id: "p-1", project_name: "Acme",
        via: "watch", script: "watches/check.ts", lang: "ts", runner: "goja", status: "timeout",
        error: "script timeout after 30s", calls: 4, call_errors: 1, duration_ms: 30000, queued_ms: 0,
        started_at: new Date().toISOString(),
      },
    ],
    page: 1, total_pages: 1, total: 1, page_size: 25,
    summary: {
      total: 10, ok: 9, failed: 1, by_status: { ok: 9, timeout: 1 }, avg_duration_ms: 850,
      calls: 40, call_errors: 1,
      top_users: [{ key: "u-1", label: "Alice", runs: 10, failed: 1 }],
      top_projects: [{ key: "p-1", label: "Acme", runs: 10, failed: 1 }],
      top_scripts: [{ key: "watches/check.ts", label: "watches/check.ts", runs: 10, failed: 1 }],
      top_errors: [{ key: "script timeout after 30s", label: "script timeout after 30s", runs: 1, failed: 1 }],
    },
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  history.replaceState({}, "", "/admin/audit");
  vi.mocked(api.getCodeRuns).mockResolvedValue(makeResult());
});

describe("CodeRuns", () => {
  it("shows the summary and the failed run with its error", async () => {
    render(CodeRuns);
    await waitFor(() => expect(screen.getAllByText("watches/check.ts").length).toBeGreaterThan(0));
    expect(screen.getAllByText("10").length).toBeGreaterThan(0);
    expect(screen.getByText("(10.0%)")).toBeTruthy();
    expect(screen.getAllByText("script timeout after 30s").length).toBeGreaterThan(0);
  });

  it("filters by a top project and keeps it in the URL", async () => {
    render(CodeRuns);
    await waitFor(() => expect(screen.getAllByText("Acme").length).toBeGreaterThan(0));
    await fireEvent.click(screen.getAllByRole("button", { name: "Acme" })[0]);
    await waitFor(() => expect(vi.mocked(api.getCodeRuns)).toHaveBeenLastCalledWith(expect.objectContaining({ project: "p-1", page: 1 })));
    expect(window.location.search).toContain("project=p-1");
    expect(window.location.search).toContain("tab=codemode");
  });
});
