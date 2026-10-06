import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import PluginsAdmin from "../PluginsAdmin.svelte";
import * as api from "$lib/api.js";
import type { InstalledPlugin } from "$lib/api.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));
vi.mock("@wick-fe/common-stores", () => ({ toastError: vi.fn(), toastOk: vi.fn() }));

function plugin(over: Partial<InstalledPlugin>): InstalledPlugin {
  return { key: "x", name: "X", kind: "connector", version: "1.0.0", enabled: true, detail_path: "/connectors/x",
    origin: "local", update_available: false, last_health_ok: false, ...over };
}

const installed: InstalledPlugin[] = [
  plugin({ key: "loki", name: "Loki", origin: "official", update_available: true, latest_version: "1.2.0",
    source_name: "Official wick", source_url: "https://example.test/plugins.json", download_url: "https://dl.example.test/loki.zip" }),
  plugin({ key: "echo", name: "Echo", kind: "tool", origin: "source", source_id: "s1", source_name: "Acme",
    source_url: "https://github.com/acme/plugins", enabled: false }),
  plugin({ key: "up", name: "Uploaded", kind: "job", origin: "upload" }),
  plugin({ key: "hand", name: "Handmade", kind: "service", origin: "local" }),
];

function mockAll(isAdmin: boolean) {
  vi.mocked(api.listPluginSources).mockResolvedValue({ sources: [], is_admin: isAdmin } as never);
  vi.mocked(api.listAvailablePlugins).mockResolvedValue({ available: [], is_admin: isAdmin });
  vi.mocked(api.listInstalledPlugins).mockResolvedValue({
    plugins: installed, is_admin: isAdmin, official: { url: "https://example.test/plugins.json", plugins: 3 },
  });
  vi.mocked(api.listPlugins).mockResolvedValue({
    installed: [], is_admin: isAdmin,
    available: [
      { key: "loki", name: "Loki", description: "", version: "1.2.0", installed: false, enabled: false, arch_ok: true, signed: "none" },
      { key: "notion", name: "Notion", description: "", version: "1.0.0", installed: false, enabled: false, arch_ok: true, signed: "none" },
    ],
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, "", "/");
});

async function openMenu(key: string) {
  await fireEvent.click(await screen.findByLabelText(`Actions for ${key}`));
}

describe("PluginsAdmin installed tab", () => {
  it("opens on Installed with a compact table, origin badges and stat chips", async () => {
    mockAll(true);
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("installed-row")).toHaveLength(4));
    const chips = screen.getByTestId("installed-summary").textContent ?? "";
    expect(chips).toContain("4 installed");
    expect(chips).toContain("3 active");
    expect(chips).toContain("1 update");
    expect(chips).toContain("1 official");
    const badges = screen.getAllByTestId("origin-badge").map((b) => b.textContent);
    expect(badges).toEqual(["Acme", "Local", "Official", "Upload"]); // sorted by name: Echo, Handmade, Loki, Uploaded
    expect(screen.getByTestId("update-badge").textContent).toContain("v1.2.0");
  });

  it("marks a sleeping service with a Sleeping badge", async () => {
    mockAll(true);
    vi.mocked(api.listServicePlugins).mockResolvedValue([
      { key: "hand", name: "Handmade", version: "1.0.0", path: "/x/hand/", status: { state: "sleeping", restarts: 0 }, routes: [], callback_revoked: false },
    ]);
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getByTestId("sleeping-badge").textContent).toBe("Sleeping"));
    expect(screen.getAllByTestId("sleeping-badge")).toHaveLength(1);
  });

  it("searches name/key with a debounce and keeps it in the query string", async () => {
    mockAll(true);
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("installed-row")).toHaveLength(4));
    await fireEvent.input(screen.getByTestId("plugin-search"), { target: { value: "lok" } });
    await waitFor(() => expect(screen.getAllByTestId("installed-row")).toHaveLength(1));
    await waitFor(() => expect(window.location.search).toContain("q=lok"));
    await fireEvent.input(screen.getByTestId("plugin-search"), { target: { value: "zzz" } });
    await waitFor(() => expect(screen.getByTestId("empty-state")).toBeTruthy());
    await fireEvent.click(screen.getByText("Reset search and filters"));
    await waitFor(() => expect(screen.getAllByTestId("installed-row")).toHaveLength(4));
  });

  it("focuses search on /", async () => {
    mockAll(true);
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("installed-row")).toHaveLength(4));
    await fireEvent.keyDown(window, { key: "/" });
    expect(document.activeElement).toBe(screen.getByTestId("plugin-search"));
  });

  it("filters by kind, origin and status with counts, and by stat chips", async () => {
    mockAll(true);
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("installed-row")).toHaveLength(4));
    const origin = screen.getByLabelText("Origin") as HTMLSelectElement;
    expect(origin.textContent).toContain("Local (1)");
    await fireEvent.change(origin, { target: { value: "local" } });
    expect(screen.getAllByTestId("installed-row")).toHaveLength(1);
    await fireEvent.change(screen.getByLabelText("Kind"), { target: { value: "connector" } });
    expect(screen.getByText("No plugin matches these filters.")).toBeTruthy();
    await fireEvent.click(screen.getByText("Reset search and filters"));
    await fireEvent.click(screen.getByText("1 disabled"));
    expect(screen.getAllByTestId("installed-row")).toHaveLength(1);
    expect(screen.getByText("Echo")).toBeTruthy();
    expect(window.location.search).toContain("status=disabled");
  });

  it("sorts updates first", async () => {
    mockAll(true);
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("installed-row")).toHaveLength(4));
    expect(screen.getAllByTestId("installed-row")[0].textContent).toContain("Echo");
    await fireEvent.change(screen.getByLabelText("Sort"), { target: { value: "update" } });
    expect(screen.getAllByTestId("installed-row")[0].textContent).toContain("Loki");
  });

  it("renders a connector and a tool that share a key", async () => {
    mockAll(true);
    vi.mocked(api.listInstalledPlugins).mockResolvedValue({
      plugins: [plugin({ key: "loki", name: "Loki" }), plugin({ key: "loki", name: "Loki Tool", kind: "tool" })],
      is_admin: true, official: { url: "https://example.test/plugins.json", plugins: 3 },
    });
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("installed-row")).toHaveLength(2));
    expect(screen.getByText("Loki Tool")).toBeTruthy();
  });

  it("updates from the row menu through the streaming endpoint", async () => {
    mockAll(true);
    vi.mocked(api.updatePluginStream).mockResolvedValue();
    render(PluginsAdmin);
    await openMenu("loki");
    await fireEvent.click(await screen.findByText("Update to v1.2.0"));
    await waitFor(() => expect(api.updatePluginStream).toHaveBeenCalledWith("loki", expect.any(Function)));
  });

  it("shows the shared update progress (same phases as the connector page) while streaming", async () => {
    mockAll(true);
    let release!: () => void;
    vi.mocked(api.updatePluginStream).mockImplementation(async (_key, onProgress) => {
      onProgress({ phase: "downloading", pct: 42 });
      await new Promise<void>((r) => (release = r));
    });
    render(PluginsAdmin);
    await openMenu("loki");
    await fireEvent.click(await screen.findByText("Update to v1.2.0"));
    await waitFor(() => expect(screen.getAllByText("Downloading… 42%").length).toBeGreaterThan(0));
    release();
    await waitFor(() => expect(screen.queryByText("Downloading… 42%")).toBeNull());
  });

  it("uninstalls a connector only after confirming", async () => {
    mockAll(true);
    vi.mocked(api.removePlugin).mockResolvedValue(undefined as never);
    render(PluginsAdmin);
    await openMenu("loki");
    await fireEvent.click(await screen.findByText("Uninstall…"));
    expect(api.removePlugin).not.toHaveBeenCalled();
    await fireEvent.click(await screen.findByRole("button", { name: "Uninstall" }));
    await waitFor(() => expect(api.removePlugin).toHaveBeenCalledWith("loki", "connector"));
  });

  it("offers Uninstall for every kind, with a per-kind confirm text", async () => {
    mockAll(true);
    vi.mocked(api.removePlugin).mockResolvedValue(undefined as never);
    render(PluginsAdmin);
    for (const key of ["echo", "up", "hand"]) {
      await openMenu(key);
      expect(screen.getByText("Uninstall…")).toBeTruthy();
      await fireEvent.keyDown(document, { key: "Escape" });
    }
    await openMenu("hand");
    await fireEvent.click(await screen.findByText("Uninstall…"));
    expect(await screen.findByText(/Stops the service first/)).toBeTruthy();
    await fireEvent.click(await screen.findByRole("button", { name: "Uninstall" }));
    await waitFor(() => expect(api.removePlugin).toHaveBeenCalledWith("hand", "service"));
  });

  it("bulk-selects rows and disables the selected connectors", async () => {
    mockAll(true);
    vi.mocked(api.setPluginEnabled).mockResolvedValue(undefined as never);
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("installed-row")).toHaveLength(4));
    await fireEvent.click(screen.getByLabelText("Select page"));
    expect(screen.getByTestId("bulk-bar").textContent).toContain("4 selected");
    await fireEvent.click(screen.getByRole("button", { name: "Disable" }));
    await waitFor(() => expect(api.setPluginEnabled).toHaveBeenCalledTimes(1));
    expect(api.setPluginEnabled).toHaveBeenCalledWith("loki", false);
  });

  it("checks one source from the row menu and every source from the toolbar", async () => {
    mockAll(true);
    vi.mocked(api.listPluginSources).mockResolvedValue({
      sources: [{ id: "s1", name: "Acme", enabled: true }, { id: "s2", name: "Off", enabled: false }], is_admin: true,
    } as never);
    vi.mocked(api.checkPluginSource).mockResolvedValue({} as never);
    render(PluginsAdmin);
    await openMenu("echo");
    await fireEvent.click(await screen.findByText("Check for update"));
    await waitFor(() => expect(api.checkPluginSource).toHaveBeenCalledWith("s1"));
    await waitFor(() => expect((screen.getByText("Check updates").closest("button") as HTMLButtonElement).disabled).toBe(false));
    vi.mocked(api.checkPluginSource).mockClear();
    await fireEvent.click(screen.getByText("Check updates"));
    await waitFor(() => expect(api.checkPluginSource).toHaveBeenCalledTimes(1)); // disabled source skipped
    await waitFor(() => expect(screen.getByTestId("check-summary").textContent).toBe("1 update(s) available"));
  });

  it("paginates 25 per page and resets to page 1 on search", async () => {
    mockAll(true);
    vi.mocked(api.listInstalledPlugins).mockResolvedValue({
      plugins: Array.from({ length: 30 }, (_, i) => plugin({ key: `p${String(i).padStart(2, "0")}`, name: `P${String(i).padStart(2, "0")}` })),
      is_admin: true, official: { url: "https://example.test/plugins.json", plugins: 0 },
    });
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("installed-row")).toHaveLength(25));
    expect(screen.getAllByTestId("pager-info")[0].textContent).toBe("1–25 of 30");
    await fireEvent.click(screen.getByText("Next ›"));
    expect(screen.getAllByTestId("installed-row")).toHaveLength(5);
    await waitFor(() => expect(window.location.search).toContain("page=2"));
    await fireEvent.input(screen.getByTestId("plugin-search"), { target: { value: "P0" } });
    await waitFor(() => expect(screen.getAllByTestId("pager-info")[0].textContent).toBe("1–10 of 10"));
  });

  it("is read-only for non-admins", async () => {
    mockAll(false);
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("installed-row")).toHaveLength(4));
    expect(screen.queryByText("Check updates")).toBeNull();
    expect(screen.queryByLabelText("Select page")).toBeNull();
    await openMenu("loki");
    expect(screen.queryByText("Update to v1.2.0")).toBeNull();
    expect(screen.queryByText("Uninstall…")).toBeNull();
    expect(screen.getByText("Open details")).toBeTruthy();
  });

  it("shows the official catalog as a source and every catalog plugin in the Marketplace, with search", async () => {
    mockAll(true);
    render(PluginsAdmin);
    await fireEvent.click(await screen.findByText(/^Sources/));
    expect(screen.getByTestId("official-source-row").textContent).toContain("https://example.test/plugins.json");
    await fireEvent.click(screen.getByText(/^Marketplace/));
    const cards = screen.getAllByTestId("market-card");
    expect(cards.map((c) => c.dataset.state)).toEqual(["update", "install"]); // Loki installed (official), Notion not
    expect(cards[1].textContent).toContain("Notion");
    await fireEvent.input(screen.getByTestId("plugin-search"), { target: { value: "nothing" } });
    await waitFor(() => expect(screen.queryAllByTestId("market-card")).toHaveLength(0));
  });
});

describe("PluginsAdmin available empty state", () => {
  it("explains a failing source", async () => {
    mockAll(true);
    vi.mocked(api.listPlugins).mockResolvedValue({ installed: [], is_admin: true, available: [] });
    vi.mocked(api.listPluginSources).mockResolvedValue({
      sources: [{ id: "s1", name: "Acme", enabled: true, last_status: "error", last_error: "status 404", last_check_at: "2026-10-01T00:00:00Z" }],
      is_admin: true,
    } as never);
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getByTestId("source-status").textContent).toContain("Acme · error"));
    await fireEvent.click(screen.getByText(/^Marketplace/));
    expect(screen.getByTestId("available-empty").textContent).toContain("Acme failed its last check (status 404)");
  });
});

describe("PluginsAdmin add-source Test", () => {
  const steps = (fail: boolean) => [
    { n: 1, name: "Reachable", status: "ok", message: "reached example.test" },
    { n: 2, name: "Auth", status: "ok", message: "no auth needed for a URL source" },
    { n: 3, name: "Index", status: fail ? "fail" : "ok", message: fail ? "status 404" : "1 plugin(s): echo v1.0.0" },
  ];

  async function openLinkForm() {
    mockAll(true);
    render(PluginsAdmin);
    await fireEvent.click(await screen.findByText("Add new plugin"));
    await fireEvent.click(screen.getByText("Link"));
    await fireEvent.input(screen.getByPlaceholderText("https://example.com/plugins.json"), { target: { value: "https://example.test/plugins.json" } });
    await fireEvent.input(screen.getByPlaceholderText("base64 ed25519 — pins signatures"), { target: { value: "PUBKEY" } });
    await fireEvent.click(screen.getByLabelText("Auto-update"));
  }

  it("tests without saving, then Add sends the same values", async () => {
    await openLinkForm();
    vi.mocked(api.testPluginSourceInput).mockResolvedValue({ steps: steps(false) } as never);
    vi.mocked(api.savePluginSource).mockResolvedValue({ id: "s9" } as never);
    vi.mocked(api.checkPluginSource).mockResolvedValue({} as never);
    await fireEvent.click(screen.getByText("Test"));
    await waitFor(() => expect(screen.getByTestId("form-check").textContent).toContain("Check passed"));
    expect(api.savePluginSource).not.toHaveBeenCalled();
    const tested = vi.mocked(api.testPluginSourceInput).mock.calls[0][0];
    expect(tested).toMatchObject({ type: "url", url: "https://example.test/plugins.json", pub_key: "PUBKEY", auto_update: true });
    await fireEvent.click(screen.getByText("Add source"));
    await waitFor(() => expect(api.savePluginSource).toHaveBeenCalledWith(tested, undefined));
  });

  it("keeps the form and shows the failing step inline when Add is rejected", async () => {
    await openLinkForm();
    const err = Object.assign(new Error("Index: status 404"), { body: JSON.stringify({ error: "Index: status 404", steps: steps(true) }) });
    vi.mocked(api.savePluginSource).mockRejectedValue(err);
    await fireEvent.click(screen.getByText("Add source"));
    await waitFor(() => expect(screen.getByTestId("form-check").textContent).toContain("Index: status 404"));
    expect(screen.getByTestId("form-test").textContent).toContain("status 404");
    expect((screen.getByPlaceholderText("https://example.com/plugins.json") as HTMLInputElement).value).toBe("https://example.test/plugins.json");
    expect((screen.getByPlaceholderText("base64 ed25519 — pins signatures") as HTMLInputElement).value).toBe("PUBKEY");
    expect((screen.getByLabelText("Auto-update") as HTMLInputElement).checked).toBe(true);
    expect(api.checkPluginSource).not.toHaveBeenCalled();
  });
});

describe("PluginsAdmin marketplace", () => {
  function mockMarket(isAdmin: boolean) {
    mockAll(isAdmin);
    vi.mocked(api.listPluginSources).mockResolvedValue({
      sources: [{ id: "s1", name: "Acme", enabled: true, last_check_at: "2026-10-01T00:00:00Z" }], is_admin: isAdmin,
    } as never);
    const a = (over: Partial<api.AvailablePlugin>): api.AvailablePlugin =>
      ({ source_id: "s1", source_name: "Acme", key: "x", kind: "tool", name: "X", version: "1.0.0", arch_ok: true, os_arch: [], ...over });
    vi.mocked(api.listAvailablePlugins).mockResolvedValue({
      is_admin: isAdmin,
      available: [
        a({ key: "echo", name: "Echo", version: "1.0.0", installed_version: "1.0.0" }), // installed from s1
        a({ key: "up", name: "Uploaded", kind: "job", version: "2.0.0", installed_version: "1.0.0" }), // uploaded copy
        a({ key: "arm", name: "Arm only", kind: "connector", arch_ok: false }),
      ],
    });
  }
  const states = () => Object.fromEntries(screen.getAllByTestId("market-card").map((c) => [c.textContent?.match(/\b(Echo|Uploaded|Arm only|Loki|Notion)\b/)?.[1], c.dataset.state]));

  it("lists installed plugins with their state and the right action", async () => {
    mockMarket(true);
    window.history.replaceState(null, "", "/?tab=available"); // old links land on Marketplace
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("market-card")).toHaveLength(5));
    expect(states()).toEqual({ Echo: "installed", Uploaded: "other", "Arm only": "no-arch", Loki: "update", Notion: "install" });
    const card = (name: string) => screen.getAllByTestId("market-card").find((c) => c.textContent?.includes(name))!;
    expect(card("Uploaded").textContent).toContain("Installed v1.0.0 (from Upload)");
    expect(card("Uploaded").textContent).not.toContain("Install ");
    expect(card("Arm only").textContent).toContain("No build for this host");
    expect(card("Loki").textContent).toContain("Update to v1.2.0");
    expect(card("Notion").textContent).toContain("Install");
    expect(screen.getByTestId("market-summary").textContent).toContain("2 not installed");
    expect(screen.getByText(/^Marketplace/).textContent).toContain("5");
  });

  it("filters by install state and source", async () => {
    mockMarket(true);
    window.history.replaceState(null, "", "/?tab=marketplace");
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("market-card")).toHaveLength(5));
    await fireEvent.change(screen.getByLabelText("Install state"), { target: { value: "update" } });
    expect(Object.keys(states())).toEqual(["Loki"]);
    await fireEvent.change(screen.getByLabelText("Install state"), { target: { value: "not-installed" } });
    expect(Object.keys(states()).sort()).toEqual(["Arm only", "Notion"]);
    await fireEvent.change(screen.getByLabelText("Source"), { target: { value: "official" } });
    expect(Object.keys(states())).toEqual(["Notion"]);
    await waitFor(() => expect(window.location.search).toContain("avail=not-installed"));
  });

  it("uninstalls from the card menu; installs and updates from the card", async () => {
    mockMarket(true);
    vi.mocked(api.removePlugin).mockResolvedValue(undefined as never);
    vi.mocked(api.installPlugin).mockResolvedValue(undefined as never);
    window.history.replaceState(null, "", "/?tab=marketplace");
    render(PluginsAdmin);
    await fireEvent.click(await screen.findByLabelText("Actions for Acme echo"));
    await fireEvent.click(await screen.findByText("Uninstall…"));
    expect(await screen.findByText(/Stops the tool/)).toBeTruthy();
    await fireEvent.click(await screen.findByRole("button", { name: "Uninstall" }));
    await waitFor(() => expect(api.removePlugin).toHaveBeenCalledWith("echo", "tool"));
    const btn = screen.getByRole("button", { name: "Install" }) as HTMLButtonElement;
    await waitFor(() => expect(btn.disabled).toBe(false));
    await fireEvent.click(btn);
    await waitFor(() => expect(api.installPlugin).toHaveBeenCalledWith("notion"));
  });

  it("hides Install, Update and Uninstall from non-admins", async () => {
    mockMarket(false);
    window.history.replaceState(null, "", "/?tab=marketplace");
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("market-card")).toHaveLength(5));
    expect(screen.queryByRole("button", { name: "Install" })).toBeNull();
    expect(screen.queryByText("Update to v1.2.0")).toBeNull();
    await fireEvent.click(screen.getByLabelText("Actions for Acme echo"));
    expect(screen.getByText("Open details")).toBeTruthy();
    expect(screen.queryByText("Uninstall…")).toBeNull();
  });
});
