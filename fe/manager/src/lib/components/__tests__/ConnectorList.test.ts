import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import ConnectorList from "../ConnectorList.svelte";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";
import * as stores from "@wick-fe/common-stores";
import * as oauth from "../connectorOAuth.js";
import * as instanceOauth from "../custom/mcpInstanceOAuth.js";
import type { ConnectorList as ConnectorListType } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));
vi.mock("@wick-fe/common-stores", () => ({ toastOk: vi.fn(), toastError: vi.fn() }));
vi.mock("../connectorOAuth.js", () => ({ startConnectorOAuth: vi.fn() }));
vi.mock("../custom/mcpInstanceOAuth.js", () => ({ startInstanceOAuth: vi.fn() }));

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

  /* The header button is connector-wide ("Re-sync all") and distinct from the
     per-row "Re-sync tools", which probes under that instance's own account. */
  it("shows the Re-sync all button and connection chip for a custom MCP connector", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ custom: true, mcp: true, mcp_status: "connected" }));
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Slack");
    expect(screen.getByRole("button", { name: "Re-sync all" })).toBeTruthy();
    expect(screen.getByText("Connected")).toBeTruthy();
  });

  it("re-syncs MCP tools through the per-connector endpoint", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(makeData({ custom: true, mcp: true, mcp_status: "disconnected" }));
    vi.mocked(api.resyncMcpTools).mockResolvedValue({ ok: true, operations: 9 });
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Slack");
    await fireEvent.click(screen.getByRole("button", { name: "Re-sync all" }));
    await waitFor(() => expect(api.resyncMcpTools).toHaveBeenCalledWith("slack"));
  });

  it("hides the Re-sync all button for a non-MCP connector", async () => {
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Slack");
    expect(screen.queryByRole("button", { name: "Re-sync all" })).toBeNull();
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

/* Custom MCP connectors: the definition is admin-removable and renameable,
   and every instance carries its OWN account + tool catalog — so Connect /
   Re-connect and Re-sync are per-row, not connector-wide. */
describe("ConnectorList custom MCP connector", () => {
  const defID = "def-1";

  function customData(over: Partial<ConnectorListType> = {}): ConnectorListType {
    return makeData({
      key: "n8n_new",
      name: "n8n-new",
      custom: true,
      custom_source: "mcp",
      def_id: defID,
      mcp: true,
      rows: [
        { id: "row-a", label: "Prod", disabled: false, status: "ready", rate_limit_rpm: 0, tags: [] },
      ],
      ...over,
    } as Partial<ConnectorListType>);
  }

  /* The header kebab is admin-only, so every test here needs the admin flag
     the component reads from listPlugins. */
  function asAdmin() {
    vi.mocked(api.listPlugins).mockResolvedValue({ installed: [], available: [], is_admin: true });
  }

  async function openHeaderMenu() {
    await fireEvent.click(await screen.findByRole("button", { name: "Connector actions" }));
  }

  it("offers Rename and Delete for a custom connector", async () => {
    asAdmin();
    vi.mocked(api.getConnector).mockResolvedValue(customData());
    render(ConnectorList, { connectorKey: "n8n_new" });
    await screen.findByText("Prod");
    await openHeaderMenu();

    expect(screen.getByText("Rename connector")).toBeTruthy();
    expect(screen.getByText("Delete connector")).toBeTruthy();
  });

  it("does not offer Delete for a built-in connector", async () => {
    asAdmin();
    vi.mocked(api.getConnector).mockResolvedValue(makeData());
    render(ConnectorList, { connectorKey: "slack" });
    await screen.findByText("Prod");
    await openHeaderMenu();

    expect(screen.queryByText("Delete connector")).toBeNull();
    expect(screen.queryByText("Rename connector")).toBeNull();
  });

  it("renames the definition through the rename endpoint", async () => {
    asAdmin();
    vi.mocked(api.getConnector).mockResolvedValue(customData());
    vi.mocked(api.renameCustomDef).mockResolvedValue(undefined);
    render(ConnectorList, { connectorKey: "n8n_new" });
    await screen.findByText("Prod");
    await openHeaderMenu();
    await fireEvent.click(screen.getByText("Rename connector"));

    const input = screen.getByLabelText("Connector display name") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "n8n prod" } });
    await fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(api.renameCustomDef).toHaveBeenCalledWith(defID, "n8n prod"));
  });

  /* Delete cascades instances + credentials, so it stays disabled until the
     connector's name is typed back exactly. */
  it("requires the typed name before deleting the definition", async () => {
    asAdmin();
    vi.mocked(api.getConnector).mockResolvedValue(customData());
    vi.mocked(api.deleteCustomDef).mockResolvedValue(undefined);
    render(ConnectorList, { connectorKey: "n8n_new" });
    await screen.findByText("Prod");
    await openHeaderMenu();
    await fireEvent.click(screen.getByText("Delete connector"));

    /* The menu item and the modal's confirm share a label — the confirm is
       the last one rendered. */
    const deleteButtons = screen.getAllByRole("button", { name: "Delete connector" });
    const confirmBtn = deleteButtons[deleteButtons.length - 1] as HTMLButtonElement;
    expect(confirmBtn.disabled).toBe(true);

    const input = screen.getByLabelText("Type the connector name to confirm deletion");
    await fireEvent.input(input, { target: { value: "n8n-new" } });
    await waitFor(() => expect(confirmBtn.disabled).toBe(false));

    await fireEvent.click(confirmBtn);
    await waitFor(() => expect(api.deleteCustomDef).toHaveBeenCalledWith(defID));
  });

  /* Connecting is a row action, so it lives in the kebab alongside
     History/Disable/Delete — not as a button floating outside the menu. */
  it("offers Connect inside the row menu when no account is attached", async () => {
    const row = {
      id: "row-a", label: "Prod", disabled: false, status: "ready", rate_limit_rpm: 0, tags: [],
      mcp_auth: { connected: false, account: "", start_url: "/manager/connectors/custom/mcp-servers/connect?instance_id=row-a" },
    };
    vi.mocked(api.getConnector).mockResolvedValue(customData({ rows: [row] } as Partial<ConnectorListType>));
    vi.mocked(instanceOauth.startInstanceOAuth).mockReturnValue({ promise: Promise.resolve(), cancel: vi.fn() });
    render(ConnectorList, { connectorKey: "n8n_new" });
    await screen.findByText("Prod");

    // Nothing outside the menu before it is opened.
    expect(screen.queryByRole("button", { name: "Connect" })).toBeNull();
    expect(screen.getByText("Not connected")).toBeTruthy();

    await openRowMenu();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Connect" }));
    /* The MCP instance flow has its own popup contract (own BroadcastChannel,
       token persisted server-side) — it must NOT reuse the generic connector
       SSO helper, whose signal the MCP callback never sends. */
    expect(instanceOauth.startInstanceOAuth).toHaveBeenCalledWith(
      "/manager/connectors/custom/mcp-servers/connect?instance_id=row-a",
    );
    expect(oauth.startConnectorOAuth).not.toHaveBeenCalled();
  });

  /* The popup-closed fallback fires even when the user dismisses the window,
     so success is claimed only if the reloaded row really is connected. */
  it("does not claim success when the login was abandoned", async () => {
    const row = {
      id: "row-a", label: "Prod", disabled: false, status: "ready", rate_limit_rpm: 0, tags: [],
      mcp_auth: { connected: false, account: "", start_url: "/manager/connectors/custom/mcp-servers/connect?instance_id=row-a" },
    };
    vi.mocked(api.getConnector).mockResolvedValue(customData({ rows: [row] } as Partial<ConnectorListType>));
    vi.mocked(instanceOauth.startInstanceOAuth).mockReturnValue({ promise: Promise.resolve(), cancel: vi.fn() });
    render(ConnectorList, { connectorKey: "n8n_new" });
    await screen.findByText("Prod");

    await openRowMenu();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Connect" }));

    // Row comes back still unconnected — no "connected" toast.
    await waitFor(() => expect(api.getConnector).toHaveBeenCalledTimes(2));
    expect(stores.toastOk).not.toHaveBeenCalled();
  });

  it("reports the connected account after a successful login", async () => {
    const before = {
      id: "row-a", label: "Prod", disabled: false, status: "ready", rate_limit_rpm: 0, tags: [],
      mcp_auth: { connected: false, account: "", start_url: "/manager/connectors/custom/mcp-servers/connect?instance_id=row-a" },
    };
    const after = { ...before, mcp_auth: { connected: true, account: "yoga@abc.com", start_url: before.mcp_auth.start_url } };
    vi.mocked(api.getConnector)
      .mockResolvedValueOnce(customData({ rows: [before] } as Partial<ConnectorListType>))
      .mockResolvedValue(customData({ rows: [after] } as Partial<ConnectorListType>));
    vi.mocked(instanceOauth.startInstanceOAuth).mockReturnValue({ promise: Promise.resolve(), cancel: vi.fn() });
    render(ConnectorList, { connectorKey: "n8n_new" });
    await screen.findByText("Prod");

    await openRowMenu();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Connect" }));

    await waitFor(() => expect(stores.toastOk).toHaveBeenCalledWith("Connected as yoga@abc.com"));
  });

  it("shows the account inline and offers Re-connect in the row menu", async () => {
    const row = {
      id: "row-a", label: "Prod", disabled: false, status: "ready", rate_limit_rpm: 0, tags: [],
      mcp_auth: { connected: true, account: "yoga@abc.com", start_url: "/manager/connectors/custom/mcp-servers/connect?instance_id=row-a" },
    };
    vi.mocked(api.getConnector).mockResolvedValue(customData({ rows: [row] } as Partial<ConnectorListType>));
    render(ConnectorList, { connectorKey: "n8n_new" });
    await screen.findByText("Prod");

    // The identity is status — it stays visible without opening the menu.
    expect(screen.getByText("yoga@abc.com")).toBeTruthy();

    await openRowMenu();
    expect(screen.getByRole("menuitem", { name: "Re-connect" })).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: "Connect" })).toBeNull();
  });

  it("omits the connect item when the caller may not configure the row", async () => {
    const row = {
      id: "row-a", label: "Prod", disabled: false, status: "ready", rate_limit_rpm: 0, tags: [],
      mcp_auth: { connected: false, account: "", start_url: "" },
    };
    vi.mocked(api.getConnector).mockResolvedValue(customData({ rows: [row] } as Partial<ConnectorListType>));
    render(ConnectorList, { connectorKey: "n8n_new" });
    await screen.findByText("Prod");

    await openRowMenu();
    expect(screen.queryByRole("menuitem", { name: "Connect" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: "Re-connect" })).toBeNull();
  });

  /* Auth health is per instance: one row's dead token says nothing about
     another's, so the flag and the probe both live on the row. */
  function rowWithAuth(over: Record<string, unknown> = {}) {
    return {
      id: "row-a", label: "Prod", disabled: false, status: "ready", rate_limit_rpm: 0, tags: [],
      mcp_auth: {
        connected: true,
        account: "yoga@abc.com",
        start_url: "/manager/connectors/custom/mcp-servers/connect?instance_id=row-a",
        test_url: "/manager/api/connectors/n8n_new/row-a/test-auth",
        ...over,
      },
    };
  }

  it("flags an expired token in red instead of showing a healthy account chip", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(
      customData({ rows: [rowWithAuth({ expired: true })] } as Partial<ConnectorListType>),
    );
    render(ConnectorList, { connectorKey: "n8n_new" });
    await screen.findByText("Prod");

    expect(screen.getByText(/Auth failed/)).toBeTruthy();
    // The bare account chip must not also render — that would read as healthy.
    expect(screen.queryByText("yoga@abc.com")).toBeNull();
  });

  it("does not flag a token that still has a refresh path", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(
      customData({ rows: [rowWithAuth({ expired: false })] } as Partial<ConnectorListType>),
    );
    render(ConnectorList, { connectorKey: "n8n_new" });
    await screen.findByText("Prod");

    expect(screen.queryByText(/Auth failed/)).toBeNull();
    expect(screen.getByText("yoga@abc.com")).toBeTruthy();
  });

  it("flags the row when the auth probe refuses the credentials", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(
      customData({ rows: [rowWithAuth()] } as Partial<ConnectorListType>),
    );
    vi.mocked(api.testInstanceAuth).mockResolvedValue({
      ok: false, error: "401 Unauthorized", latency_ms: 120, tools: 0,
    });
    render(ConnectorList, { connectorKey: "n8n_new" });
    await screen.findByText("Prod");

    await openRowMenu();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Test auth" }));

    await waitFor(() => expect(api.testInstanceAuth).toHaveBeenCalledWith("n8n_new", "row-a"));
    await waitFor(() => expect(screen.getByText(/Auth failed/)).toBeTruthy());
    expect(stores.toastError).toHaveBeenCalledWith("Auth failed", "401 Unauthorized");
  });

  it("marks the row healthy when the auth probe succeeds", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(
      customData({ rows: [rowWithAuth()] } as Partial<ConnectorListType>),
    );
    vi.mocked(api.testInstanceAuth).mockResolvedValue({
      ok: true, error: "", latency_ms: 87, tools: 28,
    });
    render(ConnectorList, { connectorKey: "n8n_new" });
    await screen.findByText("Prod");

    await openRowMenu();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Test auth" }));

    await waitFor(() => expect(screen.getByText("Auth OK")).toBeTruthy());
    expect(stores.toastOk).toHaveBeenCalledWith("Auth OK — 28 tool(s), 87ms");
    expect(screen.queryByText(/Auth failed/)).toBeNull();
  });

  it("hides Test auth when the caller may not configure the row", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(
      customData({ rows: [rowWithAuth({ test_url: "" })] } as Partial<ConnectorListType>),
    );
    render(ConnectorList, { connectorKey: "n8n_new" });
    await screen.findByText("Prod");

    await openRowMenu();
    expect(screen.queryByRole("menuitem", { name: "Test auth" })).toBeNull();
  });

  /* The probe must run under THIS row's account — a server can expose a
     different tool set per connected identity. */
  it("re-syncs tools scoped to the instance from the row menu", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(customData());
    vi.mocked(api.resyncMcpTools).mockResolvedValue({ ok: true, operations: 28 });
    render(ConnectorList, { connectorKey: "n8n_new" });
    await screen.findByText("Prod");

    await fireEvent.click(screen.getByRole("button", { name: "Actions for Prod" }));
    await fireEvent.click(screen.getByText("Re-sync tools"));

    await waitFor(() => expect(api.resyncMcpTools).toHaveBeenCalledWith("n8n_new", "row-a"));
  });
});
