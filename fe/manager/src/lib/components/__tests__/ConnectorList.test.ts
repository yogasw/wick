import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ConnectorList from "../ConnectorList.svelte";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";
import type { ConnectorList as ConnectorListType } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));

function makeData(over: Partial<ConnectorListType> = {}): ConnectorListType {
  return {
    key: "slack",
    name: "Slack",
    description: "Slack connector",
    icon: "💬",
    fixed: false,
    op_count: 3,
    custom: false,
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
    await fireEvent.click(screen.getByText("Prod"));
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
});
