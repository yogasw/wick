import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/svelte";
import HostAccess from "../HostAccess.svelte";
import HostAccessCard from "../HostAccessCard.svelte";
import * as api from "$lib/api.js";
import type { PluginHostAccess, HostAccessScope } from "$lib/api.js";

vi.mock("$lib/api.js");

function scope(over: Partial<HostAccessScope>): HostAccessScope {
  return { scope: "users.read", label: "Read users", description: "List users.", declared: true, known: true, status: "allowed",
    default_allow: true, destructive: false, sensitive: false, declarative: false, calls_24h: 0, failed_24h: 0, denied_24h: 0, ...over };
}

/* One plugin with every resource kind, an unknown scope and a blocked item. */
function fixture(over: Partial<PluginHostAccess> = {}): PluginHostAccess {
  return {
    key: "syncer", kind: "job", version: "1.2.0", declares: true, blocked_all: false, revoked: false, item_count: 6,
    calls_24h: 3, failed_24h: 2, denied_24h: 1, last_call: "2026-10-11T11:00:00Z",
    scopes: [
      scope({}),
      scope({ scope: "rest.write", label: "Change via wick API", status: "off-by-default", default_allow: false, destructive: true, resource: "rest",
        items: [{ item: "POST /api/tickets", blocked: false, destructive: true }] }),
      scope({ scope: "db.read", label: "Read wick database", status: "allowed", overlay: "allow", default_allow: false, resource: "db",
        items: [{ item: "users:read", blocked: false, destructive: false }, { item: "tickets:read", blocked: true, destructive: false }] }),
      scope({ scope: "connectors.execute", label: "Run connector operations", resource: "connectors",
        items: [{ item: "slack.send_message", blocked: false, destructive: true }] }),
      scope({ scope: "config.read_secret", label: "Read other secrets", status: "off-by-default", default_allow: false, sensitive: true, resource: "config",
        items: [{ item: "job:notion-ticket-sync/token", blocked: false, destructive: false }] }),
      scope({ scope: "tickets.read", label: "Read tickets", declared: false, status: "undeclared", calls_24h: 2 }),
      scope({ scope: "datadir", label: "Keep a data folder", declarative: true }),
      scope({ scope: "future.scope", label: undefined, known: false, status: "unknown", reason: "declared by the plugin but not known to this wick version" }),
    ],
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.listHostAccess).mockResolvedValue([fixture()]);
  vi.mocked(api.listHostCalls).mockResolvedValue({
    calls: [{ ts: "2026-10-11T11:00:00Z", plugin: "syncer", kind: "job", rpc: "Query", scope: "db.read", item: "tickets:read", user_id: "u1", source: "cron", ok: false, code: "PermissionDenied", duration_ms: 4 }],
    total: 1, page: 1, limit: 50,
  });
});

describe("HostAccess", () => {
  it("renders every scope with its status, items, colours and the unknown note", async () => {
    render(HostAccess);
    await waitFor(() => expect(screen.getByTestId("host-plugin-syncer")).toBeTruthy());
    const status = (s: string) => within(screen.getByTestId(`host-scope-${s}`)).getByTestId("scope-status").textContent;
    expect(status("users.read")).toBe("allowed");
    expect(status("rest.write")).toBe("off by default");
    expect(status("tickets.read")).toBe("undeclared");
    expect(status("future.scope")).toBe("unknown");
    expect(screen.getByTestId("unknown-note").textContent).toContain("not known to this wick version");
    expect(within(screen.getByTestId("host-scope-future.scope")).queryByRole("switch")).toBeNull();
    expect(within(screen.getByTestId("host-scope-datadir")).queryByRole("switch")).toBeNull();
    expect(screen.getByTestId("host-plugin-syncer").textContent).toContain("2 failed (1 denied)");
    expect(screen.getByText("POST /api/tickets").className).toContain("text-red-600");
    expect(screen.getByText("job:notion-ticket-sync/token").className).toContain("text-cau-400");
    expect(screen.getByText("slack.send_message").className).toContain("text-red-600");
    expect(screen.getByRole("button", { name: "Allow tickets:read" })).toBeTruthy();
    expect(screen.getByRole("switch", { name: "rest.write off" }).getAttribute("aria-checked")).toBe("false");
    await waitFor(() => expect(screen.getAllByTestId("host-call")).toHaveLength(1));
    expect(screen.getByTestId("host-call").textContent).toContain("PermissionDenied");
  });

  it("toggle, item block/allow, block all and revoke call the API", async () => {
    vi.mocked(api.setHostScope).mockResolvedValue(fixture());
    vi.mocked(api.setHostItem).mockResolvedValue(fixture());
    vi.mocked(api.setHostBlockAll).mockResolvedValue(fixture({ blocked_all: true }));
    vi.mocked(api.setHostRevoked).mockResolvedValue({ access: fixture({ blocked_all: true, revoked: true }), dropped: 1, changed: true });
    render(HostAccess);
    await waitFor(() => expect(screen.getByTestId("host-plugin-syncer")).toBeTruthy());
    await fireEvent.click(screen.getByRole("switch", { name: "rest.write off" }));
    expect(api.setHostScope).toHaveBeenCalledWith("syncer", "rest.write", true);
    await waitFor(() => expect(screen.getByRole("button", { name: "Block users:read" })).not.toHaveProperty("disabled", true));
    await fireEvent.click(screen.getByRole("button", { name: "Block users:read" }));
    expect(api.setHostItem).toHaveBeenCalledWith("syncer", "db.read", "users:read", true);
    await waitFor(() => expect(screen.getByRole("button", { name: "Allow tickets:read" })).not.toHaveProperty("disabled", true));
    await fireEvent.click(screen.getByRole("button", { name: "Allow tickets:read" }));
    expect(api.setHostItem).toHaveBeenCalledWith("syncer", "db.read", "tickets:read", false);
    await waitFor(() => expect(screen.getByRole("button", { name: "Use default" })).not.toHaveProperty("disabled", true));
    await fireEvent.click(screen.getByRole("button", { name: "Use default" }));
    expect(api.setHostScope).toHaveBeenCalledWith("syncer", "db.read", null);
    await waitFor(() => expect(screen.getByRole("button", { name: "Block all host access" })).not.toHaveProperty("disabled", true));
    await fireEvent.click(screen.getByRole("button", { name: "Block all host access" }));
    expect(api.setHostBlockAll).toHaveBeenCalledWith("syncer", true);
    await waitFor(() => expect(screen.getByTestId("blocked-all")).toBeTruthy());
    await fireEvent.click(screen.getByRole("button", { name: "Revoke host tokens" }));
    await fireEvent.click(await screen.findByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(api.setHostRevoked).toHaveBeenCalledWith("syncer", true));
    await waitFor(() => expect(screen.getByTestId("revoked")).toBeTruthy());
    vi.mocked(api.setHostRevoked).mockResolvedValue({ access: fixture({ revoked: false }), dropped: 0, changed: true });
    await fireEvent.click(screen.getByRole("button", { name: "Restore host access" }));
    await waitFor(() => expect(api.setHostRevoked).toHaveBeenLastCalledWith("syncer", false));
    await waitFor(() => expect(screen.queryByTestId("revoked")).toBeNull());
  });

  it("audit filters reload the calls", async () => {
    render(HostAccess);
    await waitFor(() => expect(api.listHostCalls).toHaveBeenCalledWith({ plugin: "", failed: false, page: 1, limit: 50 }));
    await fireEvent.click(screen.getByRole("checkbox"));
    await waitFor(() => expect(api.listHostCalls).toHaveBeenLastCalledWith({ plugin: "", failed: true, page: 1, limit: 50 }));
    const sel = await screen.findByRole("combobox", { name: "Plugin" });
    await waitFor(() => expect(within(sel).getByRole("option", { name: "syncer" })).toBeTruthy());
    await fireEvent.change(sel, { target: { value: "syncer" } });
    await waitFor(() => expect(api.listHostCalls).toHaveBeenLastCalledWith({ plugin: "syncer", failed: true, page: 1, limit: 50 }));
  });

  it("shows why the page is unavailable (503 host API not started)", async () => {
    vi.mocked(api.listHostAccess).mockRejectedValue(new Error("host API not started"));
    render(HostAccess);
    await waitFor(() => expect(screen.getByTestId("host-access-error").textContent).toContain("host API not started"));
  });

  it("filter narrows the plugins by key", async () => {
    render(HostAccess, { filter: "nomatch" });
    await waitFor(() => expect(screen.getByText("No installed plugin uses the Host API.")).toBeTruthy());
  });
});

describe("HostAccessCard", () => {
  const installed = (isAdmin: boolean) =>
    vi.mocked(api.listInstalledPlugins).mockResolvedValue({ plugins: [{ key: "syncer" } as api.InstalledPlugin], official: {} as api.OfficialCatalog, is_admin: isAdmin });

  it("summarises scopes and links to the Host access tab", async () => {
    installed(true);
    vi.mocked(api.getHostAccess).mockResolvedValue(fixture({ revoked: true }));
    render(HostAccessCard, { pluginKey: "syncer" });
    await waitFor(() => expect(screen.getByTestId("host-access-card")).toBeTruthy());
    expect(screen.getByText("What it can do")).toBeTruthy();
    expect(screen.getByTestId("host-access-summary").textContent).toBe("8 scopes · 5 allowed · 6 items");
    expect(screen.getByTestId("card-revoked")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Manage host access" }).getAttribute("href")).toBe("/admin/plugins?tab=host&q=syncer");
  });

  it("sends no host request for a built-in or a non-admin", async () => {
    installed(true);
    render(HostAccessCard, { pluginKey: "slack" }); // built-in: not installed
    await waitFor(() => expect(api.listInstalledPlugins).toHaveBeenCalledTimes(1));
    await new Promise((r) => setTimeout(r, 10));
    installed(false);
    render(HostAccessCard, { pluginKey: "syncer" }); // non-admin
    await new Promise((r) => setTimeout(r, 10));
    expect(api.getHostAccess).not.toHaveBeenCalled();
    expect(screen.queryByTestId("host-access-card")).toBeNull();
  });
});
