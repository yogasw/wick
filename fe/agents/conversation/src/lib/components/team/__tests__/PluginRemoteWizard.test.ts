import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

let sources = [
  { key: "a2a_repeater", name: "A2A repeater", description: "Echo bot behind A2A", version: "0.1.0", state: "running" },
  { key: "relay", name: "Relay", version: "1.2.0", state: "backoff" },
];
const list = vi.fn(() => Promise.resolve(sources));
const create = vi.fn((_b: string, body: Record<string, unknown>) => Promise.resolve({ id: "p1", handle: "echo", kind: "plugin-remote", ...body }));
vi.mock("../../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/team.js")>()),
  listPluginSources: () => list(),
  createPluginRemote: (b: string, body: Record<string, unknown>) => create(b, body),
  runApi: <T,>(p: Promise<T>) => p,
}));

import PluginRemoteWizard from "../PluginRemoteWizard.svelte";

const props = () => ({ base: "/tools/agents", taken: ["captain"], onClose: vi.fn(), onCreated: vi.fn(), onType: vi.fn() });

describe("PluginRemoteWizard", () => {
  beforeEach(() => { list.mockClear(); create.mockClear(); });

  test("lists remote_source plugins; Create sends the picked key", async () => {
    const p = props();
    render(PluginRemoteWizard, p);
    await waitFor(() => expect(screen.getByTestId("plugin-source-a2a_repeater")).toBeTruthy());
    expect(screen.getByTestId("remote-source-plugin").getAttribute("aria-checked")).toBe("true");
    const btn = screen.getByRole("button", { name: "Create agent" }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    await fireEvent.click(screen.getByTestId("plugin-source-a2a_repeater"));
    await fireEvent.input(screen.getByLabelText("Handle"), { target: { value: "captain" } });
    expect(screen.getByText("That handle is taken.")).toBeTruthy();
    await fireEvent.input(screen.getByLabelText("Handle"), { target: { value: "echo" } });
    await fireEvent.click(btn);
    await waitFor(() => expect(p.onCreated).toHaveBeenCalled());
    expect(create).toHaveBeenCalledWith("/tools/agents", { plugin_key: "a2a_repeater", handle: "echo" });
  });

  test("no plugin offers a source → explains, Create stays off; source switch", async () => {
    sources = [];
    const p = props();
    render(PluginRemoteWizard, p);
    await waitFor(() => expect(screen.getByTestId("plugin-none")).toBeTruthy());
    expect((screen.getByRole("button", { name: "Create agent" }) as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.click(screen.getByTestId("remote-source-a2a"));
    expect(p.onType).toHaveBeenCalledWith("remote");
  });

  test("renders the plugin's remote_configs; an empty required field blocks Create", async () => {
    sources = [{
      key: "jules", name: "Jules", version: "0.2.0", state: "running",
      remote_configs: [
        { key: "api_key", value: "", is_secret: true, has_value: false, required: false, description: "Agent key." },
        { key: "region", value: "", is_secret: false, has_value: false, required: true },
      ],
    }];
    const p = props();
    render(PluginRemoteWizard, p);
    await waitFor(() => expect(screen.getByTestId("plugin-config-region")).toBeTruthy());
    const key = screen.getByLabelText(/api_key/) as HTMLInputElement;
    expect(key.type).toBe("password");
    expect(screen.getByTestId("plugin-config-api_key").textContent).toContain("optional");
    expect(screen.getByTestId("plugin-config-api_key").textContent).toContain("Agent key.");
    expect(screen.getByTestId("plugin-config-region").textContent).toContain("*");
    const btn = screen.getByRole("button", { name: "Create agent" }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    await fireEvent.input(key, { target: { value: "k-1" } });
    await fireEvent.input(screen.getByLabelText(/region/), { target: { value: "asia" } });
    expect(btn.disabled).toBe(false);
    await fireEvent.click(btn);
    await waitFor(() => expect(p.onCreated).toHaveBeenCalled());
    expect(create).toHaveBeenCalledWith("/tools/agents", { plugin_key: "jules", config: { api_key: "k-1", region: "asia" } });
  });

  test("only optional fields (Jules): Create works with nothing filled", async () => {
    sources = [{
      key: "jules", name: "Jules", version: "0.2.0", state: "running",
      remote_configs: [
        { key: "api_key", value: "", is_secret: true, has_value: false, required: false },
        { key: "source", value: "", is_secret: false, has_value: false, required: false },
      ],
    }];
    const p = props();
    render(PluginRemoteWizard, p);
    await waitFor(() => expect(screen.getByTestId("plugin-config-source")).toBeTruthy());
    expect(screen.getAllByTestId("plugin-config-optional")).toHaveLength(2);
    const btn = screen.getByRole("button", { name: "Create agent" }) as HTMLButtonElement;
    expect(btn.disabled).toBe(false);
    await fireEvent.click(btn);
    await waitFor(() => expect(p.onCreated).toHaveBeenCalled());
    expect(create).toHaveBeenCalledWith("/tools/agents", { plugin_key: "jules", config: { source: "" } });
  });
});
