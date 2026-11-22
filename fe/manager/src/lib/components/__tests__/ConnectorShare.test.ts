import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import ConnectorList from "../ConnectorList.svelte";
import ConnectorShareDialog from "../ConnectorShareDialog.svelte";
import * as api from "$lib/api.js";
import type { ConnectorList as ConnectorListType, ConnectorSharing } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));
vi.mock("@wick-fe/common-stores", () => ({ toastOk: vi.fn(), toastError: vi.fn() }));
vi.mock("../connectorOAuth.js", () => ({ startConnectorOAuth: vi.fn() }));
vi.mock("../custom/mcpInstanceOAuth.js", () => ({ startInstanceOAuth: vi.fn() }));

function listData(over: Partial<ConnectorListType> = {}): ConnectorListType {
  return {
    key: "acme",
    name: "Acme API",
    description: "",
    icon: "🔌",
    fixed: false,
    op_count: 1,
    custom: true,
    rows: [],
    ...over,
  };
}

function sharing(over: Partial<ConnectorSharing> = {}): ConnectorSharing {
  return {
    visibility: "restricted",
    owner: { id: "u1", name: "Owner One", email: "owner@example.com" },
    people: [],
    tags: [],
    available_tags: [],
    is_admin: false,
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.listPlugins).mockResolvedValue({ installed: [], available: [], is_admin: false } as never);
});

describe("connector Share", () => {
  it("shows Share and the Hidden chip for a restricted connector the viewer owns", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(listData({ visibility: "restricted", can_share: true }));
    render(ConnectorList, { connectorKey: "acme" });
    await screen.findByText("Acme API");
    expect(screen.getByRole("button", { name: "Share" })).toBeTruthy();
    expect(screen.getByText("🔒 Hidden")).toBeTruthy();
  });

  it("hides Share from someone who cannot share", async () => {
    vi.mocked(api.getConnector).mockResolvedValue(listData({ visibility: "everyone", can_share: false }));
    render(ConnectorList, { connectorKey: "acme" });
    await screen.findByText("Acme API");
    expect(screen.queryByRole("button", { name: "Share" })).toBeNull();
    expect(screen.queryByText("🔒 Hidden")).toBeNull();
  });

  it("owner shares by email", async () => {
    vi.mocked(api.getConnectorSharing).mockResolvedValue(sharing());
    vi.mocked(api.setConnectorSharing).mockResolvedValue(
      sharing({ people: [{ id: "u2", name: "Friend", email: "friend@example.com" }] }),
    );
    render(ConnectorShareDialog, { open: true, connectorKey: "acme", connectorName: "Acme API", onClose: vi.fn() });
    const input = await screen.findByLabelText("Email to share with");
    await fireEvent.input(input, { target: { value: "friend@example.com" } });
    await fireEvent.click(screen.getByRole("button", { name: "Share" }));
    await waitFor(() =>
      expect(api.setConnectorSharing).toHaveBeenCalledWith("acme", { action: "share_email", email: "friend@example.com" }),
    );
    expect(await screen.findByText("Friend")).toBeTruthy();
    // Owners get no tag picker and no everyone switch.
    expect(screen.queryByRole("switch")).toBeNull();
  });

  it("admin sees the everyone switch and tag picker", async () => {
    vi.mocked(api.getConnectorSharing).mockResolvedValue(
      sharing({ is_admin: true, available_tags: [{ id: "t1", name: "team-a" }] }),
    );
    render(ConnectorShareDialog, { open: true, connectorKey: "acme", connectorName: "Acme API", onClose: vi.fn() });
    expect(await screen.findByRole("switch")).toBeTruthy();
    expect(screen.getByText("Tags")).toBeTruthy();
  });
});
