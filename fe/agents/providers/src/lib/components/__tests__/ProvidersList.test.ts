import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ProvidersList from "../ProvidersList.svelte";
import * as api from "$lib/api.js";

vi.mock("$lib/api.js");
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: vi.fn(),
  toasts: { subscribe: vi.fn(() => vi.fn()) },
}));

function makeData() {
  return {
    Providers: [
      {
        Instance: { Type: "claude", Name: "claude", Binary: "claude", Disabled: false, MaxConcurrent: 4, SendMode: "" },
        Path: "/usr/bin/claude",
        PathFound: true,
        Version: "1.2.3",
        VersionErr: "",
        Probing: false,
        Hooks: {},
        HookEnabled: {},
        Cap: { Used: 1, Max: 4, Unlimited: false },
      },
      {
        Instance: { Type: "openai", Name: "gpt4", Binary: "", Disabled: true, MaxConcurrent: 2, SendMode: "" },
        Path: "",
        PathFound: false,
        Version: "",
        VersionErr: "binary not found",
        Probing: false,
        Hooks: {},
        HookEnabled: {},
        Cap: { Used: 0, Max: 2, Unlimited: false },
      },
    ],
    Gate: { Enabled: true, Binary: "/usr/bin/gate", Source: "config", Reason: "", Note: "Gate note", PermissionMode: "bypass", BypassLocked: false },
    Spawns: [],
    MCPClients: {
      AppName: "wick-agent",
      Clients: [
        { ID: "mcp-1", Label: "Wick MCP", Detected: true, Installed: false, Blocklisted: false, ConfigPath: "/home/x/.config" },
      ],
    },
    AutoRescan: false,
    PoolActive: 0,
    PoolQueueLen: 0,
    PoolMax: 2,
    LiveProcesses: [],
    SupportedKeys: ["claude", "openai"],
  };
}

beforeEach(() => {
  vi.mocked(api.apiGetProviders).mockResolvedValue(makeData());
  vi.mocked(api.apiRescanAll).mockResolvedValue(undefined);
  vi.mocked(api.apiRescanOne).mockResolvedValue(undefined);
  vi.mocked(api.apiGateToggle).mockResolvedValue(undefined);
  vi.mocked(api.apiGateModes).mockResolvedValue(undefined);
  vi.mocked(api.apiDeleteProvider).mockResolvedValue(undefined);
  vi.mocked(api.apiAutoRescanToggle).mockResolvedValue(undefined);
  vi.mocked(api.apiMCPInstall).mockResolvedValue(undefined);
  vi.mocked(api.apiMCPUninstall).mockResolvedValue(undefined);
  vi.mocked(api.apiCreateProvider).mockResolvedValue(undefined);
});

describe("ProvidersList", () => {
  it("renders provider cards after load", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    expect(await screen.findByText("openai/gpt4")).toBeTruthy();
    expect(screen.getByText("claude/claude")).toBeTruthy();
  });

  it("shows version for found provider", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    expect(screen.getByText("1.2.3")).toBeTruthy();
  });

  it("shows disabled label for disabled provider", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    expect(screen.getAllByText("disabled").length).toBeGreaterThan(0);
  });

  it("shows Configured stat card", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    expect(screen.getByText("Configured")).toBeTruthy();
    expect(screen.getByText("Active Slots")).toBeTruthy();
  });

  it("shows Command Gate master section", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    const gates = screen.getAllByText("Command Gate");
    expect(gates.length).toBeGreaterThan(0);
    expect(screen.getByText("enabled")).toBeTruthy();
  });

  it("shows MCP Wick section with app badge", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText(/MCP Wick/);
    expect(screen.getByText("wick-agent")).toBeTruthy();
    const mcpBtn = screen.getByText(/MCP Wick/);
    fireEvent.click(mcpBtn);
    expect(await screen.findByText("Wick MCP")).toBeTruthy();
  });

  it("calls onNavigate when Detail is clicked", async () => {
    const onNavigate = vi.fn();
    render(ProvidersList, { props: { onNavigate, base: "" } });
    await screen.findByText("openai/gpt4");
    const btns = screen.getAllByText("Detail");
    fireEvent.click(btns[0]);
    expect(onNavigate).toHaveBeenCalledWith("claude", "claude");
  });

  it("calls apiRescanAll when Rescan all clicked", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    fireEvent.click(screen.getByText("Rescan all"));
    expect(api.apiRescanAll).toHaveBeenCalled();
  });

  it("calls apiGateToggle when Turn off clicked", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    fireEvent.click(screen.getByText("Turn off"));
    expect(api.apiGateToggle).toHaveBeenCalled();
  });

  it("calls apiAutoRescanToggle and shows off state", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    const btn = screen.getByText("Auto-rescan: off");
    fireEvent.click(btn);
    expect(api.apiAutoRescanToggle).toHaveBeenCalled();
  });

  it("opens the Add Custom modal", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    fireEvent.click(screen.getByText("+ Add Custom"));
    expect(await screen.findByText("New Provider Instance")).toBeTruthy();
  });
});
