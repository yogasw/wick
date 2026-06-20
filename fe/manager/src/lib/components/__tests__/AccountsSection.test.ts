import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import AccountsSection from "../AccountsSection.svelte";
import * as api from "$lib/api.js";
import type { ConnectorAccount, ConnectorOp, ConnectorOAuthMeta } from "$lib/types.js";

vi.mock("$lib/api.js");
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
