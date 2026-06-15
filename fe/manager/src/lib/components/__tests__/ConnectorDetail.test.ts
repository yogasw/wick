import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ConnectorDetail from "../ConnectorDetail.svelte";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";
import type { ConnectorDetail as DetailType } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));

function makeData(over: Partial<DetailType> = {}): DetailType {
  return {
    key: "slack",
    name: "Slack",
    icon: "💬",
    id: "row-a",
    label: "Prod",
    disabled: false,
    rate_limit_rpm: 0,
    has_health_check: true,
    can_configure: true,
    fields: [
      { key: "api_url", type: "url", value: "https://x.test", options: "", required: true, is_secret: false, has_value: true, description: "", visible_when: "", env_override: "" },
    ],
    operations: [
      { key: "send", name: "Send", description: "Send a message", destructive: false, enabled: true, system_disabled: false, system_disabled_reason: "" },
      { key: "del", name: "Delete", description: "Delete a message", destructive: true, enabled: false, system_disabled: false, system_disabled_reason: "" },
    ],
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getConnectorRow).mockResolvedValue(makeData());
});

describe("ConnectorDetail", () => {
  it("renders identity, label, configs, and operations", async () => {
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    expect(await screen.findByText("row-a")).toBeTruthy();
    expect(screen.getByText("api_url")).toBeTruthy();
    expect(screen.getByText("Send")).toBeTruthy();
    expect(screen.getByText("Delete")).toBeTruthy();
    expect(screen.getByText("destructive")).toBeTruthy();
  });

  it("saves the label", async () => {
    vi.mocked(api.setConnectorLabel).mockResolvedValue(undefined);
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("row-a");
    const input = screen.getByLabelText("Connector label");
    await fireEvent.input(input, { target: { value: "Renamed" } });
    await fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await Promise.resolve();
    expect(api.setConnectorLabel).toHaveBeenCalledWith("slack", "row-a", "Renamed");
  });

  it("runs a health check and shows the result banner", async () => {
    vi.mocked(api.runHealthCheck).mockResolvedValue({ ok: true, newly_locked: [], newly_cleared: [] });
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("row-a");
    await fireEvent.click(screen.getByRole("button", { name: "Check Permissions" }));
    expect(await screen.findByText(/No changes/)).toBeTruthy();
  });

  it("hides the health-check button when unsupported", async () => {
    vi.mocked(api.getConnectorRow).mockResolvedValue(makeData({ has_health_check: false }));
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("row-a");
    expect(screen.queryByRole("button", { name: "Check Permissions" })).toBeNull();
  });

  it("deletes the row and navigates back to the list", async () => {
    vi.mocked(api.deleteConnectorRow).mockResolvedValue(undefined);
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("row-a");
    await fireEvent.click(screen.getByRole("button", { name: "Delete row" }));
    await fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    await Promise.resolve();
    expect(api.deleteConnectorRow).toHaveBeenCalledWith("slack", "row-a");
    expect(router.push).toHaveBeenCalledWith("/connectors/slack");
  });

  it("renders read-only fields when can_configure is false", async () => {
    vi.mocked(api.getConnectorRow).mockResolvedValue(makeData({ can_configure: false }));
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("row-a");
    expect((screen.getByLabelText("Connector label") as HTMLInputElement).disabled).toBe(true);
  });
});
