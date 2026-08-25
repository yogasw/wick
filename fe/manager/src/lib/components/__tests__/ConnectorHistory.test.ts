import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/svelte";
import ConnectorHistory from "../ConnectorHistory.svelte";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";
import type { HistoryResult, HistoryRun } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));

function makeRun(over: Partial<HistoryRun> = {}): HistoryRun {
  return {
    id: "run-1",
    operation_key: "send",
    source: "test",
    status: "success",
    user_id: "u-1",
    user_name: "Alice",
    error_msg: "",
    latency_ms: 12,
    http_status: 200,
    ip_address: "127.0.0.1",
    user_agent: "vitest",
    request_json: '{"channel":"#general"}',
    response_json: '{"ok":"yes"}',
    started_at: new Date().toISOString(),
    ...over,
  };
}

function makeResult(over: Partial<HistoryResult> = {}): HistoryResult {
  return {
    key: "slack",
    name: "Slack",
    id: "row-a",
    label: "Prod",
    runs: [makeRun()],
    ops: [{ key: "send", name: "Send" }],
    users: [{ id: "u-1", name: "Alice" }],
    page: 1,
    total_pages: 1,
    total: 1,
    page_size: 10,
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  history.replaceState({}, "", "/modules/manager/app/connectors/slack/row-a/history");
  vi.mocked(api.getConnectorHistory).mockResolvedValue(makeResult());
});

describe("ConnectorHistory", () => {
  it("renders the filter bar and the run table", async () => {
    render(ConnectorHistory, { connectorKey: "slack", connectorId: "row-a" });
    expect(await screen.findByText("Run history")).toBeTruthy();
    const table = screen.getByRole("table");
    expect(within(table).getByText("send")).toBeTruthy();
    expect(within(table).getByText("Alice")).toBeTruthy();
    expect(within(table).getByText("12 ms")).toBeTruthy();
  });

  it("expands a row to reveal request + response JSON", async () => {
    render(ConnectorHistory, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("Run history");
    expect(screen.queryByText("Request")).toBeNull();
    await fireEvent.click(screen.getByText("send"));
    expect(await screen.findByText("Request")).toBeTruthy();
    expect(screen.getByText("Response")).toBeTruthy();
    expect(screen.getByText(/run-1/)).toBeTruthy();
  });

  it("re-fetches with the source filter and resets to page 1", async () => {
    render(ConnectorHistory, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("Run history");
    vi.mocked(api.getConnectorHistory).mockClear();
    /* Filter selects render in order: op, source, status, user. */
    const sourceSelect = screen.getAllByRole("combobox")[1];
    await fireEvent.change(sourceSelect, { target: { value: "mcp" } });
    expect(api.getConnectorHistory).toHaveBeenCalledWith(
      "slack",
      "row-a",
      expect.objectContaining({ source: "mcp", page: 1 }),
    );
    expect(window.location.search).toBe("?source=mcp");
  });

  it("paginates to the next page", async () => {
    vi.mocked(api.getConnectorHistory).mockResolvedValue(
      makeResult({ total: 13, total_pages: 2, page: 1 }),
    );
    render(ConnectorHistory, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("Run history");
    vi.mocked(api.getConnectorHistory).mockClear();
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    expect(api.getConnectorHistory).toHaveBeenCalledWith(
      "slack",
      "row-a",
      expect.objectContaining({ page: 2 }),
    );
    expect(window.location.search).toBe("?page=2");
  });

  it("shows an empty state when no runs match", async () => {
    vi.mocked(api.getConnectorHistory).mockResolvedValue(makeResult({ runs: [], total: 0 }));
    render(ConnectorHistory, { connectorKey: "slack", connectorId: "row-a" });
    expect(await screen.findByText("No runs match the current filters.")).toBeTruthy();
  });

  it("navigates to the test runner via the Test runner button", async () => {
    render(ConnectorHistory, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("Run history");
    await fireEvent.click(screen.getByRole("button", { name: "Test runner" }));
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/row-a/test");
  });

  it("renders the error message for failed runs", async () => {
    vi.mocked(api.getConnectorHistory).mockResolvedValue(
      makeResult({ runs: [makeRun({ status: "error", error_msg: "kaboom" })] }),
    );
    render(ConnectorHistory, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("Run history");
    const table = screen.getByRole("table");
    expect(within(table).getByText("kaboom")).toBeTruthy();
  });

  it("renders the H1 at the legacy 1.375rem size", async () => {
    render(ConnectorHistory, { connectorKey: "slack", connectorId: "row-a" });
    const h1 = await screen.findByRole("heading", { level: 1, name: "Run history" });
    expect(h1.className).toContain("text-[1.375rem]");
  });

  it("shows the 'Showing X–Y of N run(s)' range", async () => {
    vi.mocked(api.getConnectorHistory).mockResolvedValue(makeResult({ total: 13, total_pages: 2, page: 2, page_size: 10, runs: [makeRun()] }));
    history.replaceState({}, "", "/modules/manager/app/connectors/slack/row-a/history?page=2");
    render(ConnectorHistory, { connectorKey: "slack", connectorId: "row-a" });
    expect(await screen.findByText("Showing 11–13 of 13 run(s)")).toBeTruthy();
  });

  it("shows the user-agent line in the expanded row", async () => {
    render(ConnectorHistory, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("Run history");
    await fireEvent.click(screen.getByText("send"));
    expect(await screen.findByText(/UA:/)).toBeTruthy();
    expect(screen.getByText("vitest")).toBeTruthy();
  });

  it("deep-links a completed run to the test panel with ?op= and ?prefill=", async () => {
    render(ConnectorHistory, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("Run history");
    await fireEvent.click(screen.getByText("send"));
    await fireEvent.click(await screen.findByRole("button", { name: "Retry in test panel" }));
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/row-a/test?op=send&prefill=run-1");
  });

  it("hides the retry link for running runs", async () => {
    vi.mocked(api.getConnectorHistory).mockResolvedValue(makeResult({ runs: [makeRun({ status: "running" })] }));
    render(ConnectorHistory, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("Run history");
    await fireEvent.click(screen.getByText("send"));
    expect(screen.queryByRole("button", { name: "Retry in test panel" })).toBeNull();
  });
});
