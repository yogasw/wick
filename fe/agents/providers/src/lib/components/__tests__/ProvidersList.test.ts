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
    Gate: { Enabled: true, Binary: "", Source: "config", Reason: "", Note: "", PermissionMode: "default", BypassLocked: false },
    Spawns: [
      {
        Path: "/tmp/spawns/abc.log",
        ProviderType: "claude",
        ProviderName: "claude",
        SessionID: "sess-abc",
        StartedAt: "2024-01-01T00:00:00Z",
        PID: 1234,
        Origin: "web",
        FirstUserMessage: "Hello world",
        Binary: "claude",
        ExitReason: "done",
      },
    ],
    MCPClients: {
      AppName: "Wick",
      Clients: [
        { ID: "mcp-1", Label: "Wick MCP", Detected: true, Installed: false, Blocklisted: false, ConfigPath: "" },
      ],
    },
    AutoRescan: false,
    PoolActive: 1,
    PoolQueueLen: 0,
    PoolMax: 4,
    LiveProcesses: [],
    SupportedKeys: ["claude", "openai"],
  };
}

beforeEach(() => {
  vi.mocked(api.apiGetProviders).mockResolvedValue(makeData());
  vi.mocked(api.apiRescanAll).mockResolvedValue(undefined);
  vi.mocked(api.apiRescanOne).mockResolvedValue(undefined);
  vi.mocked(api.apiGateToggle).mockResolvedValue(undefined);
  vi.mocked(api.apiDeleteProvider).mockResolvedValue(undefined);
  vi.mocked(api.apiProbeGate).mockResolvedValue(undefined);
  vi.mocked(api.apiAutoRescanToggle).mockResolvedValue(undefined);
  vi.mocked(api.apiMCPInstall).mockResolvedValue(undefined);
  vi.mocked(api.apiMCPUninstall).mockResolvedValue(undefined);
});

describe("ProvidersList", () => {
  it("renders provider cards after load", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    expect(await screen.findByText("gpt4")).toBeTruthy();
    const claudeNames = screen.getAllByText("claude");
    expect(claudeNames.length).toBeGreaterThan(0);
  });

  it("shows ready badge for found provider", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText("gpt4");
    expect(screen.getByText("ready")).toBeTruthy();
  });

  it("shows disabled badge for disabled provider", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText("gpt4");
    expect(screen.getByText("disabled")).toBeTruthy();
  });

  it("shows pool stats", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText("gpt4");
    expect(screen.getByText("Active")).toBeTruthy();
  });

  it("shows gate panel with Enabled state", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText("gpt4");
    expect(screen.getByText("Permission Gate")).toBeTruthy();
    expect(screen.getByText("Enabled")).toBeTruthy();
  });

  it("shows spawn log entries", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText(/Spawn Log/);
    const spawnBtn = screen.getByText(/Spawn Log/);
    fireEvent.click(spawnBtn);
    expect(await screen.findByText("sess-abc")).toBeTruthy();
  });

  it("shows MCP clients section", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText(/MCP Clients/);
    const mcpBtn = screen.getByText(/MCP Clients/);
    fireEvent.click(mcpBtn);
    expect(await screen.findByText("Wick MCP")).toBeTruthy();
  });

  it("calls onNavigate when Configure is clicked", async () => {
    const onNavigate = vi.fn();
    render(ProvidersList, { props: { onNavigate, base: "" } });
    await screen.findByText("gpt4");
    const btns = screen.getAllByText("Configure");
    fireEvent.click(btns[0]);
    expect(onNavigate).toHaveBeenCalledWith("claude", "claude");
  });

  it("calls apiRescanAll when Rescan All clicked", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText("gpt4");
    fireEvent.click(screen.getByText("Rescan All"));
    expect(api.apiRescanAll).toHaveBeenCalled();
  });

  it("calls apiGateToggle when gate button clicked", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), base: "" } });
    await screen.findByText("gpt4");
    fireEvent.click(screen.getByText("Enabled"));
    expect(api.apiGateToggle).toHaveBeenCalled();
  });
});
