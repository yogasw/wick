import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import ConnectorList from "../ConnectorList.svelte";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";
import * as stores from "@wick-fe/common-stores";
import * as oauth from "../connectorOAuth.js";
import type { ConnectorList as ConnectorListType } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));
vi.mock("@wick-fe/common-stores", () => ({ toastOk: vi.fn(), toastError: vi.fn() }));
vi.mock("../connectorOAuth.js", () => ({ startConnectorOAuth: vi.fn() }));

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

/* History/Disable/Duplicate/Delete live behind a per-row kebab (⋮) menu.
   Open the first row's menu before asserting on those items. */
async function openRowMenu(label = "Prod") {
  await fireEvent.click(screen.getByRole("button", { name: `Actions for ${label}` }));
}

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
    await openRowMenu();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Duplicate" }));
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
    await openRowMenu();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Disable" }));
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
    await openRowMenu();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Disable" }));
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

  it("shows a 'Private' chip (not Everyone) for an owner-only row", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(
      makeData({
        rows: [{ id: "row-p", label: "Mine", disabled: false, status: "ready", rate_limit_rpm: 0, tags: [], private: true }],
      }),
    );
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Mine");
    expect(screen.getByText("Private")).toBeTruthy();
    expect(screen.queryByText("Everyone")).toBeNull();
  });

  it("navigates to a row's run history via the per-row History action", async () => {
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    await openRowMenu();
    await fireEvent.click(screen.getByRole("menuitem", { name: "History" }));
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/row-a/history");
  });

  it("keeps the row actions behind a closed kebab menu until opened", async () => {
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    // Menu items are not in the DOM while the kebab is closed.
    expect(screen.queryByRole("menuitem", { name: "History" })).toBeNull();
    await openRowMenu();
    expect(screen.getByRole("menuitem", { name: "History" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Disable" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Duplicate" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Delete" })).toBeTruthy();
  });

  it("opens detail when the row body (not just the label) is clicked", async () => {
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    await fireEvent.click(screen.getByLabelText("Open Prod"));
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/row-a");
  });

  it("shows the definition-updated reload banner when the connector needs a reload", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ custom: true, needs_reload: true }));
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Slack");
    expect(screen.getByText(/Definition updated/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Reload" })).toBeTruthy();
  });

  it("reloads the definition through the per-connector endpoint", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ custom: true, needs_reload: true }));
    vi.mocked(api.reloadConnector).mockResolvedValue({ ok: true });
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Slack");
    await fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(api.reloadConnector).toHaveBeenCalledWith("slack"));
  });

  it("hides the reload banner when the connector is up to date", async () => {
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Slack");
    expect(screen.queryByText(/Definition updated/)).toBeNull();
  });

  it("shows the Re-sync tools button and connection chip for a custom MCP connector", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ custom: true, mcp: true, mcp_status: "connected" }));
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Slack");
    expect(screen.getByRole("button", { name: "Re-sync tools" })).toBeTruthy();
    expect(screen.getByText("Connected")).toBeTruthy();
  });

  it("re-syncs MCP tools through the per-connector endpoint", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ custom: true, mcp: true, mcp_status: "disconnected" }));
    vi.mocked(api.resyncMcpTools).mockResolvedValue({ ok: true, operations: 9 });
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Slack");
    await fireEvent.click(screen.getByRole("button", { name: "Re-sync tools" }));
    await waitFor(() => expect(api.resyncMcpTools).toHaveBeenCalledWith("slack"));
  });

  it("hides the Re-sync tools button for a non-MCP connector", async () => {
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Slack");
    expect(screen.queryByRole("button", { name: "Re-sync tools" })).toBeNull();
  });
});

describe("ConnectorList OAuth / SSO", () => {
  // A row with SSO ready (start_url set) and no accounts yet.
  function ssoRow() {
    return {
      id: "row-a",
      label: "Prod",
      disabled: false,
      status: "ready",
      rate_limit_rpm: 0,
      tags: [],
      enable_sso: true,
      multi_account: false,
      oauth: { display_name: "Slack", start_url: "/manager/connectors/slack/oauth/start?connector_id=row-a" },
      accounts: [],
    };
  }

  it("renders the per-row Connect button when start_url is set", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ rows: [ssoRow()] }));
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    expect(screen.getByRole("button", { name: "Connect" })).toBeTruthy();
  });

  it("hides Connect when oauth has no start_url (SSO off / not configured)", async () => {
    const row = { ...ssoRow(), oauth: { display_name: "Slack", start_url: "" } };
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ rows: [row] }));
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    expect(screen.queryByRole("button", { name: "Connect" })).toBeNull();
  });

  it("labels the button Reconnect for a single-account row that already has one", async () => {
    const row = { ...ssoRow(), accounts: [{ id: "acc-1", display_name: "yoga.setiawan", wick_user_id: "u1", disabled_ops: [], can_manage: true }] };
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ rows: [row] }));
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    expect(screen.getByRole("button", { name: "Reconnect" })).toBeTruthy();
  });

  it("labels the button '+ Connect another' for a multi-account row", async () => {
    const row = { ...ssoRow(), multi_account: true, accounts: [{ id: "acc-1", display_name: "yoga.setiawan", wick_user_id: "u1", disabled_ops: [], can_manage: true }] };
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ rows: [row] }));
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    expect(screen.getByRole("button", { name: "+ Connect another" })).toBeTruthy();
  });

  it("starts the OAuth popup and refreshes on success", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ rows: [ssoRow()] }));
    vi.mocked(oauth.startConnectorOAuth).mockReturnValue({ promise: Promise.resolve(), cancel: vi.fn() });
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    await fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    expect(oauth.startConnectorOAuth).toHaveBeenCalledWith("/manager/connectors/slack/oauth/start?connector_id=row-a");
    await waitFor(() => expect(api.getConnector).toHaveBeenCalledTimes(2));
  });

  it("renders connected-account sub-rows with a Disconnect action", async () => {
    const row = { ...ssoRow(), accounts: [{ id: "acc-1", display_name: "yoga.setiawan", wick_user_id: "u1", disabled_ops: [], can_manage: true }] };
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ rows: [row] }));
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    expect(screen.getByText("@yoga.setiawan")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Disconnect" })).toBeTruthy();
  });

  it("hides Disconnect on accounts the caller can't manage", async () => {
    const row = { ...ssoRow(), accounts: [{ id: "acc-1", display_name: "someone", wick_user_id: "u2", disabled_ops: [], can_manage: false }] };
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ rows: [row] }));
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("@someone");
    expect(screen.queryByRole("button", { name: "Disconnect" })).toBeNull();
  });

  it("disconnects an account through the per-row endpoint after confirm", async () => {
    const row = { ...ssoRow(), accounts: [{ id: "acc-1", display_name: "yoga.setiawan", wick_user_id: "u1", disabled_ops: [], can_manage: true }] };
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ rows: [row] }));
    vi.mocked(api.disconnectConnectorAccount).mockResolvedValue(undefined);
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    /* Row Disconnect opens the confirm dialog, whose confirm is also labelled
       "Disconnect" — pick the last match (the dialog button). */
    await fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
    const buttons = screen.getAllByRole("button", { name: "Disconnect" });
    await fireEvent.click(buttons[buttons.length - 1]);
    await waitFor(() => expect(api.disconnectConnectorAccount).toHaveBeenCalledWith("slack", "row-a", "acc-1"));
  });
});
