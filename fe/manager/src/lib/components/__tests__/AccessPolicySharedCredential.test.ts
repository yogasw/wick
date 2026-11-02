import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import AccessPolicySection from "../AccessPolicySection.svelte";
import * as api from "$lib/api.js";
import type { ConnectorDetail } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("@wick-fe/common-stores", () => ({ toastOk: vi.fn(), toastError: vi.fn() }));

function detail(oauth: Partial<NonNullable<ConnectorDetail["oauth"]>> = {}): ConnectorDetail {
  return {
    key: "acme", name: "Acme", icon: "", id: "row-a", label: "Prod", disabled: false, rate_limit_rpm: 0,
    has_health_check: false, can_configure: true, is_admin: true, can_manage_policy: true,
    fields: [], operations: [], categories: [], accounts: [],
    oauth: { display_name: "Acme", start_url: "", shared_credential: false, ...oauth },
    enable_sso: true, multi_account: true, allow_others_connect_sso: false, allow_others_configure: false,
    allow_others_see_accounts: false, session_config_capable: false, session_config_allowed: false,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("AccessPolicySection — shared instance credential", () => {
  it("posts the toggle on its own endpoint", async () => {
    vi.mocked(api.setConnectorSharedCredential).mockResolvedValue(true);
    render(AccessPolicySection, { connectorKey: "acme", connectorId: "row-a", data: detail() });
    await fireEvent.click(screen.getByRole("checkbox", { name: "Allow shared instance credential" }));
    await waitFor(() => expect(api.setConnectorSharedCredential).toHaveBeenCalledWith("acme", "row-a", true));
  });

  it("offers to clear a legacy token while off", async () => {
    vi.mocked(api.setConnectorSharedCredential).mockResolvedValue(false);
    render(AccessPolicySection, { connectorKey: "acme", connectorId: "row-a", data: detail({ shared_credential_stored: true }) });
    await fireEvent.click(screen.getByRole("button", { name: "Clear it" }));
    await waitFor(() => expect(api.setConnectorSharedCredential).toHaveBeenCalledWith("acme", "row-a", false));
  });

  it("hides the clear prompt when nothing is stored", () => {
    render(AccessPolicySection, { connectorKey: "acme", connectorId: "row-a", data: detail() });
    expect(screen.queryByRole("button", { name: "Clear it" })).toBeNull();
  });
});
