import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import AuditLog from "../AuditLog.svelte";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";
import type { AuditResult } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));

function makeResult(over: Partial<AuditResult> = {}): AuditResult {
  return {
    runs: [
      {
        id: "r1",
        connector_id: "c-1",
        connector_key: "slack",
        connector_name: "Prod Slack",
        operation_key: "send",
        source: "mcp",
        status: "success",
        user_id: "u-1",
        user_name: "Alice",
        latency_ms: 42,
        started_at: new Date().toISOString(),
      },
    ],
    source: "",
    status: "",
    from: "",
    to: "",
    page: 1,
    total_pages: 1,
    total: 1,
    page_size: 25,
    summary: { total: 1, succeeded: 1, errored: 0, avg_latency_ms: 42 },
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  history.replaceState({}, "", "/modules/manager/app/audit");
  vi.mocked(api.getAuditRuns).mockResolvedValue(makeResult());
});

describe("AuditLog", () => {
  it("renders resolved run rows with connector + user names", async () => {
    render(AuditLog);
    expect(await screen.findByText("Prod Slack")).toBeTruthy();
    expect(screen.getByText("Alice")).toBeTruthy();
    expect(screen.getByText("42 ms")).toBeTruthy();
  });

  it("renders the summary block", async () => {
    render(AuditLog);
    await screen.findByText("Prod Slack");
    expect(screen.getByText("1 run(s) found")).toBeTruthy();
    expect(screen.getByText("· 1 ok")).toBeTruthy();
  });

  it("re-fetches and syncs ?source= when the source filter changes", async () => {
    render(AuditLog);
    await screen.findByText("Prod Slack");
    const sourceSelect = screen.getAllByRole("combobox")[0];
    await fireEvent.change(sourceSelect, { target: { value: "mcp" } });
    await waitFor(() => expect(window.location.search).toBe("?source=mcp"));
    expect(api.getAuditRuns).toHaveBeenLastCalledWith(expect.objectContaining({ source: "mcp", page: 1 }));
  });

  it("navigates to the connector detail on row click", async () => {
    render(AuditLog);
    await screen.findByText("Prod Slack");
    await fireEvent.click(screen.getByRole("button", { name: "Prod Slack" }));
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/c-1");
  });

  it("paginates to the next page", async () => {
    vi.mocked(api.getAuditRuns).mockResolvedValue(makeResult({ total: 30, total_pages: 2 }));
    render(AuditLog);
    await screen.findByText("Prod Slack");
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    await waitFor(() => expect(window.location.search).toBe("?page=2"));
    expect(api.getAuditRuns).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 }));
  });

  it("shows an empty state when no runs match", async () => {
    vi.mocked(api.getAuditRuns).mockResolvedValue(makeResult({ runs: [], total: 0, summary: { total: 0, succeeded: 0, errored: 0, avg_latency_ms: 0 } }));
    render(AuditLog);
    expect(await screen.findByText("No runs match the current filter.")).toBeTruthy();
  });
});
