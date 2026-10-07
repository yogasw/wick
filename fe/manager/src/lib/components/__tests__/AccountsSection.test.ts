import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import AccountsSection from "../AccountsSection.svelte";
import * as api from "$lib/api.js";
import type { ConnectorAccount, ConnectorOp, ConnectorOAuthMeta } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));
vi.mock("@wick-fe/common-stores", () => ({ toastOk: vi.fn(), toastError: vi.fn() }));
vi.mock("../connectorOAuth.js", () => ({ startConnectorOAuth: vi.fn() }));

const oauth: ConnectorOAuthMeta = { display_name: "Slack", start_url: "" };
const ops: ConnectorOp[] = [
  { key: "send", name: "Send", description: "", destructive: false, enabled: true, system_disabled: false, system_disabled_reason: "", admin_only: false, category: "" },
  { key: "del", name: "Delete", description: "", destructive: true, enabled: true, system_disabled: false, system_disabled_reason: "", admin_only: false, category: "" },
];

function acc(over: Partial<ConnectorAccount> = {}): ConnectorAccount {
  return { id: "acc-1", display_name: "alice", wick_user_id: "u-1", disabled_ops: [], can_manage: true, ...over };
}

function props(over: Record<string, unknown> = {}) {
  return {
    connectorKey: "slack",
    connectorId: "row-a",
    accounts: [acc()],
    operations: ops,
    oauth,
    enableSso: true,
    multiAccount: true,
    onchanged: vi.fn(),
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("AccountsSection — per-account operations", () => {
  it("shows the Manage operations button for a manageable account", () => {
    render(AccountsSection, props());
    expect(screen.getByRole("button", { name: "Manage operations" })).toBeTruthy();
  });

  it("expands the editor and saves the disabled-ops set for the account", async () => {
    vi.mocked(api.setAccountDisabledOps).mockResolvedValue(undefined);
    render(AccountsSection, props({ accounts: [acc({ disabled_ops: [] })] }));
    await fireEvent.click(screen.getByRole("button", { name: "Manage operations" }));
    /* All ops start enabled (checked); unchecking "Send" disables it. */
    await fireEvent.click(screen.getByRole("checkbox", { name: /Send/ }));
    await fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.setAccountDisabledOps).toHaveBeenCalledWith("slack", "row-a", "acc-1", ["send"]));
  });

  it("hides Manage operations when the account is not manageable", () => {
    render(AccountsSection, props({ accounts: [acc({ can_manage: false })] }));
    expect(screen.queryByRole("button", { name: "Manage operations" })).toBeNull();
  });
});

describe("AccountsSection — per-account actions menu", () => {
  /* Disconnect used to be a bare button on the row, which made destroying a
     grant a single click and left re-connecting with no affordance at all —
     people disconnected just to re-consent. Both now live behind the row's ⋮. */
  it("keeps Disconnect behind the actions menu", async () => {
    render(AccountsSection, props());
    expect(screen.queryByRole("button", { name: "Disconnect" })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Account actions" }));
    expect(screen.getByRole("menuitem", { name: "Disconnect" })).toBeTruthy();
  });

  it("re-connects through the actions menu without disconnecting first", async () => {
    const { startConnectorOAuth } = await import("../connectorOAuth.js");
    vi.mocked(startConnectorOAuth).mockReturnValue({ promise: Promise.resolve(), cancel: vi.fn() });
    render(
      AccountsSection,
      props({ oauth: { display_name: "Slack", start_url: "/manager/connectors/slack/oauth/start?connector_id=row-a" } }),
    );
    await fireEvent.click(screen.getByRole("button", { name: "Account actions" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Re-connect" }));
    expect(startConnectorOAuth).toHaveBeenCalledWith("/manager/connectors/slack/oauth/start?connector_id=row-a");
    expect(api.disconnectConnectorAccount).not.toHaveBeenCalled();
  });

  it("disables Re-connect when the instance has no OAuth start URL", async () => {
    render(AccountsSection, props({ oauth: { display_name: "Slack", start_url: "" } }));
    await fireEvent.click(screen.getByRole("button", { name: "Account actions" }));
    expect(screen.getByRole("menuitem", { name: "Re-connect" }).hasAttribute("disabled")).toBe(true);
  });

  it("hides the header connect button on a single-account row that is already connected", () => {
    render(AccountsSection, props({ multiAccount: false }));
    expect(screen.queryByRole("button", { name: /Connect/ })).toBeNull();
  });
});

describe("AccountsSection — opening an account", () => {
  it("navigates to the account's own page from its name", async () => {
    const { push } = await import("$lib/router.js");
    render(AccountsSection, props());
    await fireEvent.click(screen.getByRole("button", { name: "Open @alice" }));
    expect(push).toHaveBeenCalledWith("/connectors/slack/row-a/accounts/acc-1");
  });
});
