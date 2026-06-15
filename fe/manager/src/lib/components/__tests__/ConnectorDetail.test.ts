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
    is_admin: false,
    fields: [
      { key: "api_url", type: "url", value: "https://x.test", options: "", required: true, is_secret: false, has_value: true, description: "", visible_when: "", env_override: "" },
    ],
    operations: [
      { key: "send", name: "Send", description: "Send a message", destructive: false, enabled: true, system_disabled: false, system_disabled_reason: "", admin_only: false },
      { key: "del", name: "Delete", description: "Delete a message", destructive: true, enabled: false, system_disabled: false, system_disabled_reason: "", admin_only: false },
    ],
    accounts: [],
    oauth: null,
    enable_sso: false,
    multi_account: false,
    allow_others_connect_sso: false,
    allow_others_configure: false,
    session_config_capable: false,
    session_config_allowed: false,
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
    /* Two Save buttons exist (label + rate limit); the label one is first. */
    await fireEvent.click(screen.getAllByRole("button", { name: "Save" })[0]);
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

  it("saves the rate limit", async () => {
    vi.mocked(api.setConnectorRateLimit).mockResolvedValue(60);
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("row-a");
    const input = screen.getByLabelText("Rate limit per minute");
    await fireEvent.input(input, { target: { value: "60" } });
    /* The second Save button is the rate-limit one. */
    const saves = screen.getAllByRole("button", { name: "Save" });
    await fireEvent.click(saves[saves.length - 1]);
    await Promise.resolve();
    expect(api.setConnectorRateLimit).toHaveBeenCalledWith("slack", "row-a", 60);
  });

  it("duplicates the row and navigates to the copy", async () => {
    vi.mocked(api.duplicateConnectorRow).mockResolvedValue("row-copy");
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("row-a");
    await fireEvent.click(screen.getByRole("button", { name: "Duplicate row" }));
    await Promise.resolve();
    expect(api.duplicateConnectorRow).toHaveBeenCalledWith("slack", "row-a");
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/row-copy");
  });

  it("toggles an operation enabled state via the toggle endpoint", async () => {
    vi.mocked(api.toggleConnectorOperation).mockResolvedValue(false);
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("row-a");
    await fireEvent.click(screen.getByLabelText("Enable Send"));
    await Promise.resolve();
    expect(api.toggleConnectorOperation).toHaveBeenCalledWith("slack", "row-a", "send", false);
  });

  it("hides operation toggles when can_configure is false", async () => {
    vi.mocked(api.getConnectorRow).mockResolvedValue(makeData({ can_configure: false }));
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("row-a");
    expect(screen.queryByLabelText("Enable Send")).toBeNull();
  });

  it("renders admin-only toggles and access policy only for admins", async () => {
    vi.mocked(api.getConnectorRow).mockResolvedValue(makeData({ is_admin: true }));
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("row-a");
    expect(screen.getByLabelText("Admin-only Send")).toBeTruthy();
    expect(screen.getByText("Access policy")).toBeTruthy();
    expect(screen.getByLabelText("Allow others to configure")).toBeTruthy();
  });

  it("renders the accounts section + connect button for OAuth connectors", async () => {
    vi.mocked(api.getConnectorRow).mockResolvedValue(
      makeData({
        oauth: { display_name: "Slack", start_url: "/manager/connectors/slack/oauth/start?connector_id=row-a" },
        enable_sso: true,
        is_admin: true,
      }),
    );
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("row-a");
    expect(screen.getByText("Connected accounts")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Connect account" })).toBeTruthy();
  });

  it("disconnects an account", async () => {
    vi.mocked(api.disconnectConnectorAccount).mockResolvedValue(undefined);
    vi.mocked(api.getConnectorRow).mockResolvedValue(
      makeData({
        oauth: { display_name: "Slack", start_url: "" },
        enable_sso: true,
        accounts: [{ id: "acc-1", display_name: "tester", wick_user_id: "u1", disabled_ops: [], can_manage: true }],
      }),
    );
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("row-a");
    /* Row Disconnect opens the confirm dialog, whose confirm is also labelled
       "Disconnect" — pick the last match (the dialog button). */
    await fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
    const buttons = screen.getAllByRole("button", { name: "Disconnect" });
    await fireEvent.click(buttons[buttons.length - 1]);
    await Promise.resolve();
    expect(api.disconnectConnectorAccount).toHaveBeenCalledWith("slack", "row-a", "acc-1");
  });

  it("saves the access policy when a toggle changes", async () => {
    vi.mocked(api.setConnectorAccessPolicy).mockResolvedValue(undefined);
    vi.mocked(api.getConnectorRow).mockResolvedValue(makeData({ is_admin: true }));
    render(ConnectorDetail, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("row-a");
    await fireEvent.click(screen.getByLabelText("Allow others to configure"));
    await Promise.resolve();
    expect(api.setConnectorAccessPolicy).toHaveBeenCalledWith(
      "slack",
      "row-a",
      expect.objectContaining({ allow_others_configure: true }),
    );
  });
});
