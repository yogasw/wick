import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import AuditLog from "../AuditLog.svelte";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";
import * as stores from "@wick-fe/common-stores";
import type { AuditResult } from "$lib/types.js";

/* pickOption drives the themed <Select> (common-ui): open its trigger,
   click the option carrying `value`. The listbox is portalled to body. */
async function pickOption(trigger: HTMLElement, value: string): Promise<void> {
  await fireEvent.click(trigger);
  const opt = document.body.querySelector<HTMLElement>(`[role="option"][data-value="${value}"]`);
  if (!opt) throw new Error(`no option ${value}`);
  await fireEvent.click(opt);
}


vi.mock("$lib/api.js", async (orig) => ({
  ...(await orig<typeof import("$lib/api.js")>()),
  getAuditRuns: vi.fn(),
  getAuditRun: vi.fn(),
}));
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));
vi.mock("@wick-fe/common-stores", () => ({ toastError: vi.fn() }));

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
    users: [{ id: "u-1", name: "Alice" }],
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
    expect(screen.getAllByText("42 ms").length).toBeGreaterThan(0);
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
    const sourceSelect = screen.getAllByTestId("wick-select-trigger")[3];
    await pickOption(sourceSelect, "mcp");
    await waitFor(() => expect(window.location.search).toBe("?source=mcp"));
    expect(api.getAuditRuns).toHaveBeenLastCalledWith(expect.objectContaining({ source: "mcp", page: 1 }));
  });

  it("opens the call in full on row click, then the connector from there", async () => {
    vi.mocked(api.getAuditRun).mockResolvedValue({
      ...makeResult().runs![0],
      session_id: "s-1", session_label: "Ops chat", project_name: "Acme",
      account_id: "", http_status: 200, ip_address: "", user_agent: "", parent_run_id: "",
      ended_at: new Date().toISOString(), error_full: "", request: '{"text":"hi"}', response: '{"ok":true}',
    });
    render(AuditLog);
    await fireEvent.click(await screen.findByText("Prod Slack"));
    await waitFor(() => expect(api.getAuditRun).toHaveBeenCalledWith("r1"));
    expect(await screen.findByText("Ops chat")).toBeTruthy();
    expect(screen.getByText("Acme")).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Prod Slack" }));
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/c-1");
  });

  it("applies the advanced filter query", async () => {
    render(AuditLog);
    await screen.findByText("Prod Slack");
    const box = screen.getByLabelText("Advanced filter") as HTMLInputElement;
    await fireEvent.input(box, { target: { value: 'connector:slack op:send,get status:error user:Alice latency>500 "rate limit"' } });
    await fireEvent.keyDown(box, { key: "Enter" });
    await waitFor(() =>
      expect(api.getAuditRuns).toHaveBeenLastCalledWith(
        expect.objectContaining({ connector: "slack", op: "send,get", status: "error", user: "u-1", min_ms: "500", error: "rate limit", page: 1 }),
      ),
    );
    expect(window.location.search).toContain("connector=slack");
    expect(window.location.search).toContain("user=u-1");
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

  it("does not flash the Loading screen on a filter-change refresh", async () => {
    render(AuditLog);
    await screen.findByText("Prod Slack");
    const sourceSelect = screen.getAllByTestId("wick-select-trigger")[3];
    await pickOption(sourceSelect, "mcp");
    expect(screen.queryByText("Loading…")).toBeNull();
    expect(screen.getByText("Prod Slack")).toBeTruthy();
    await waitFor(() => expect(api.getAuditRuns).toHaveBeenLastCalledWith(expect.objectContaining({ source: "mcp" })));
    expect(screen.queryByText("Loading…")).toBeNull();
  });

  it("renders table header cells with uppercase tracking-wide classes", async () => {
    render(AuditLog);
    await screen.findByText("Prod Slack");
    const th = screen.getByRole("columnheader", { name: "Connector" });
    expect(th?.className).toContain("uppercase");
    expect(th?.className).toContain("tracking-wide");
    expect(th?.className).toContain("text-xs");
  });

  it("toasts on a silent-refresh failure instead of showing the error screen", async () => {
    vi.mocked(api.getAuditRuns).mockResolvedValueOnce(makeResult()).mockRejectedValueOnce(new Error("refresh boom"));
    render(AuditLog);
    await screen.findByText("Prod Slack");
    const sourceSelect = screen.getAllByTestId("wick-select-trigger")[3];
    await pickOption(sourceSelect, "mcp");
    await waitFor(() => expect(stores.toastError).toHaveBeenCalledWith("Refresh failed", "refresh boom"));
    expect(screen.queryByText("refresh boom")).toBeNull();
    expect(screen.getByText("Prod Slack")).toBeTruthy();
  });
});
