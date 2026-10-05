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
});

describe("PluginsAdmin installed tab", () => {
  it("opens on Installed with origin badges and summary", async () => {
    mockAll(true);
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("installed-row")).toHaveLength(4));
    expect(screen.getByTestId("installed-summary").textContent).toContain("4 installed · 3 active · 1 official · 3 self-installed");
    const badges = screen.getAllByTestId("origin-badge").map((b) => b.textContent);
    expect(badges).toEqual(["Official wick", "Source: Acme", "Upload", "Local / unknown"]);
    expect(screen.getByText("Update to v1.2.0")).toBeTruthy();
    expect(screen.getByText("Download ↗").getAttribute("href")).toBe("https://dl.example.test/loki.zip");
  });

  it("filters by kind and origin", async () => {
    mockAll(true);
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("installed-row")).toHaveLength(4));
    await fireEvent.click(screen.getByRole("group", { name: "Origin" }).querySelectorAll("button")[4]); // Local
    expect(screen.getAllByTestId("installed-row")).toHaveLength(1);
    await fireEvent.click(screen.getByRole("group", { name: "Kind" }).querySelectorAll("button")[1]); // Connector
    expect(screen.getByText("No plugin matches these filters.")).toBeTruthy();
  });

  it("updates in-row through the streaming endpoint", async () => {
    mockAll(true);
    vi.mocked(api.updatePluginStream).mockResolvedValue();
    render(PluginsAdmin);
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
    await fireEvent.click(await screen.findByText("Update to v1.2.0"));
    await waitFor(() => expect(screen.getAllByText("Downloading… 42%").length).toBeGreaterThan(0));
    release();
    await waitFor(() => expect(screen.queryByText("Downloading… 42%")).toBeNull());
  });

  it("checks one source from its row and every source from the toolbar", async () => {
    mockAll(true);
    vi.mocked(api.listPluginSources).mockResolvedValue({
      sources: [{ id: "s1", name: "Acme", enabled: true }, { id: "s2", name: "Off", enabled: false }], is_admin: true,
    } as never);
    vi.mocked(api.checkPluginSource).mockResolvedValue({} as never);
    render(PluginsAdmin);
    await fireEvent.click(await screen.findByText("Check update"));
    await waitFor(() => expect(api.checkPluginSource).toHaveBeenCalledWith("s1"));
    // Wait for the row check (and its reload) to finish before the toolbar re-enables.
    await waitFor(() => expect((screen.getByText("Check updates").closest("button") as HTMLButtonElement).disabled).toBe(false));
    vi.mocked(api.checkPluginSource).mockClear();
    await fireEvent.click(screen.getByText("Check updates"));
    await waitFor(() => expect(api.checkPluginSource).toHaveBeenCalledTimes(1)); // disabled source skipped
  });

  it("is read-only for non-admins", async () => {
    mockAll(false);
    render(PluginsAdmin);
    await waitFor(() => expect(screen.getAllByTestId("installed-row")).toHaveLength(4));
    expect(screen.queryByText("Update to v1.2.0")).toBeNull();
    expect(screen.queryByText("Check updates")).toBeNull();
  });

  it("shows the official catalog as a source and its uninstalled plugins as available", async () => {
    mockAll(true);
    render(PluginsAdmin);
    await fireEvent.click(await screen.findByText(/^Sources/));
    expect(screen.getByTestId("official-source-row").textContent).toContain("https://example.test/plugins.json");
    await fireEvent.click(screen.getByText(/^Available/));
    const rows = screen.getAllByTestId("available-row");
    expect(rows).toHaveLength(1);
    expect(rows[0].textContent).toContain("Notion");
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
