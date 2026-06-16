import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import ConnectorList from "../ConnectorList.svelte";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";
import * as stores from "@wick-fe/common-stores";
import type { ConnectorList as ConnectorListType } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));
vi.mock("@wick-fe/common-stores", () => ({ toastOk: vi.fn(), toastError: vi.fn() }));

function makeData(over: Partial<ConnectorListType> = {}): ConnectorListType {
  return {
    key: "slack",
    name: "Slack",
    description: "Slack connector",
    icon: "💬",
    fixed: false,
    op_count: 3,
    custom: false,
    custom_source: "",
    rows: [
      { id: "row-a", label: "Prod", disabled: false, status: "ready", rate_limit_rpm: 0, tags: ["team:eng"] },
      { id: "row-b", label: "Staging", disabled: true, status: "ready", rate_limit_rpm: 0, tags: [] },
    ],
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getConnector).mockResolvedValue(makeData());
});

describe("ConnectorList", () => {
  it("renders the connector header and rows", async () => {
    render(ConnectorList, { connectorKey: "slack" });
    expect(await screen.findByText("Slack")).toBeTruthy();
    expect(screen.getByText("Prod")).toBeTruthy();
    expect(screen.getByText("Staging")).toBeTruthy();
  });

  it("shows status chips per row", async () => {
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    expect(screen.getByText("Published")).toBeTruthy();
    expect(screen.getByText("Disabled")).toBeTruthy();
  });

  it("navigates to the detail route on row click", async () => {
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    await fireEvent.click(screen.getByLabelText("Open Prod"));
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/row-a");
  });

  it("creates a row and navigates to it", async () => {
    vi.mocked(api.createConnectorRow).mockResolvedValue("new-id");
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    await fireEvent.click(screen.getByRole("button", { name: "+ New row" }));
    await Promise.resolve();
    expect(api.createConnectorRow).toHaveBeenCalledWith("slack");
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/new-id");
  });

  it("duplicates a row and navigates to the copy", async () => {
    vi.mocked(api.duplicateConnectorRow).mockResolvedValue("row-copy");
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    await fireEvent.click(screen.getAllByRole("button", { name: "Duplicate" })[0]);
    await Promise.resolve();
    expect(api.duplicateConnectorRow).toHaveBeenCalledWith("slack", "row-a");
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/row-copy");
  });

  it("hides + New row for fixed connectors", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ fixed: true }));
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Slack");
    expect(screen.queryByRole("button", { name: "+ New row" })).toBeNull();
  });

  it("renders an error state on failure", async () => {
    vi.mocked(api.getConnector).mockRejectedValueOnce(new Error("nope"));
    render(ConnectorList, { connectorKey: "slack" });
    expect(await screen.findByText("nope")).toBeTruthy();
  });

  it("refreshes silently after disable — no Loading flash, rows stay mounted", async () => {
    vi.mocked(api.toggleConnectorDisabled).mockResolvedValue(true);
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    await fireEvent.click(screen.getAllByRole("button", { name: "Disable" })[0]);
    await waitFor(() => expect(api.toggleConnectorDisabled).toHaveBeenCalledWith("slack", "row-a"));
    await waitFor(() => expect(api.getConnector).toHaveBeenCalledTimes(2));
    expect(screen.queryByText("Loading…")).toBeNull();
    expect(screen.getByText("Prod")).toBeTruthy();
  });

  it("toasts on a silent-refresh failure instead of replacing the page", async () => {
    vi.mocked(api.toggleConnectorDisabled).mockResolvedValue(false);
    vi.mocked(api.getConnector).mockResolvedValueOnce(makeData()).mockRejectedValueOnce(new Error("refresh boom"));
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    await fireEvent.click(screen.getAllByRole("button", { name: "Disable" })[0]);
    await waitFor(() => expect(stores.toastError).toHaveBeenCalledWith("Refresh failed", "refresh boom"));
    expect(screen.queryByText("refresh boom")).toBeNull();
    expect(screen.getByText("Prod")).toBeTruthy();
  });

  it("renders the connector H1 heading", async () => {
    render(ConnectorList, { connectorKey: "slack" });
    const h1 = await screen.findByRole("heading", { level: 1, name: "Slack" });
    expect(h1.className).toContain("text-lg");
  });

  it("renders the Custom badge for custom connectors", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ custom: true }));
    render(ConnectorList, { connectorKey: "slack" });
    const badge = await screen.findByText("Custom");
    expect(badge.className).toContain("text-green-500");
  });

  it("shows an 'Everyone' dashed chip for rows without tags", async () => {
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Staging");
    const chip = screen.getByText("Everyone");
    expect(chip.className).toContain("border-dashed");
  });

  it("navigates to a row's run history via the per-row History action", async () => {
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    await fireEvent.click(screen.getAllByRole("button", { name: "History" })[0]);
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/row-a/history");
  });

  it("opens detail when the row body (not just the label) is clicked", async () => {
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    await fireEvent.click(screen.getByLabelText("Open Prod"));
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/row-a");
  });
});
