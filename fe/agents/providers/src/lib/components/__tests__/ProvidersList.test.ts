import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import ProvidersList from "../ProvidersList.svelte";
import { expectCardRhythm } from "./cardRhythm.js";
import * as api from "$lib/api.js";
import * as tty from "$lib/logintty.js";
import * as mb from "$lib/managedbin.js";
import type { ProvidersListResponse, ProviderConnection } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/logintty.js");
vi.mock("$lib/managedbin.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("$lib/managedbin.js")>()),
  apiManagedList: vi.fn(async () => ({ types: [], isAdmin: true })),
}));
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: vi.fn(),
  toasts: { subscribe: vi.fn(() => vi.fn()) },
}));

function makeData(): ProvidersListResponse {
  return {
    IsAdmin: true,
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
        CanManage: true,
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
        CanManage: true,
      },
    ],
    Gate: { Enabled: true, Binary: "/usr/bin/gate", Source: "config", Reason: "", Note: "Gate note", PermissionMode: "bypass", BypassLocked: false },
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
  // ProvidersList embeds <RecentSpawns>, which fetches sessions on mount.
  vi.mocked(api.apiGetSessions).mockResolvedValue({ Sessions: [], Page: 1, HasNext: false, Total: 0 });
  vi.mocked(api.apiRescanAll).mockResolvedValue(undefined);
  vi.mocked(api.apiRescanOne).mockResolvedValue(undefined);
  vi.mocked(api.apiGateToggle).mockResolvedValue(undefined);
  vi.mocked(api.apiGateModes).mockResolvedValue(undefined);
  vi.mocked(api.apiDeleteProvider).mockResolvedValue(undefined);
  vi.mocked(api.apiAutoRescanToggle).mockResolvedValue(undefined);
  vi.mocked(tty.apiLoginTTYUsageRefresh).mockResolvedValue({ accepted: true, checking: true, waitS: 0 });
  vi.mocked(api.apiMCPInstall).mockResolvedValue(undefined);
  vi.mocked(api.apiMCPUninstall).mockResolvedValue(undefined);
  vi.mocked(api.apiCreateProvider).mockResolvedValue(undefined);
  vi.mocked(api.apiHookEnable).mockResolvedValue(undefined);
  vi.mocked(api.apiHookDisable).mockResolvedValue(undefined);
  vi.mocked(api.apiHookCheck).mockResolvedValue(undefined);
  vi.mocked(api.apiGetConnections).mockResolvedValue([]);
});

describe("ProvidersList", () => {
  it("renders provider cards after load", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    expect(await screen.findByText("openai/gpt4")).toBeTruthy();
    expect(screen.getByText("claude/claude")).toBeTruthy();
  });

  it("shows version for found provider", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    expect(screen.getByText("1.2.3")).toBeTruthy();
  });

  it("shows disabled label for disabled provider", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    expect(screen.getAllByText("disabled").length).toBeGreaterThan(0);
  });

  it("shows Configured stat card", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    expect(screen.getByText("Configured")).toBeTruthy();
    expect(screen.getByText("Active Slots")).toBeTruthy();
  });

  it("shows Command Gate master section", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    const gates = screen.getAllByText("Command Gate");
    expect(gates.length).toBeGreaterThan(0);
    expect(screen.getByText("enabled")).toBeTruthy();
  });

  it("shows MCP Wick section with app badge", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText(/MCP Wick/);
    expect(screen.getByText("wick-agent")).toBeTruthy();
    const mcpBtn = screen.getByText(/MCP Wick/);
    fireEvent.click(mcpBtn);
    expect(await screen.findByText("Wick MCP")).toBeTruthy();
  });

  it("calls onNavigate when Detail is clicked", async () => {
    const onNavigate = vi.fn();
    render(ProvidersList, { props: { onNavigate, onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    const btns = screen.getAllByText("Detail");
    fireEvent.click(btns[0]);
    expect(onNavigate).toHaveBeenCalledWith("claude", "claude");
  });

  it("calls apiRescanAll when Rescan all clicked", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    fireEvent.click(screen.getByText("Rescan all"));
    expect(api.apiRescanAll).toHaveBeenCalled();
  });

  it("calls apiGateToggle when Turn off clicked", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    fireEvent.click(screen.getByText("Turn off"));
    expect(api.apiGateToggle).toHaveBeenCalled();
  });

  it("calls apiAutoRescanToggle and shows off state", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    const btn = screen.getByText("Auto-rescan: off");
    fireEvent.click(btn);
    expect(api.apiAutoRescanToggle).toHaveBeenCalled();
  });

  it("opens the Add Custom modal", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    fireEvent.click(screen.getByText("+ Add Custom"));
    expect(await screen.findByText("New Provider Instance")).toBeTruthy();
  });
});

describe("ProvidersList - hook capability section", () => {
  it("shows Enable button when gate on and intent off, calls apiHookEnable", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "/wick" } });
    await screen.findByText("openai/gpt4");
    const enableBtns = screen.getAllByText("Enable");
    fireEvent.click(enableBtns[0]);
    expect(api.apiHookEnable).toHaveBeenCalledWith("/wick", "claude", "claude", "PreToolUse");
  });

  it("shows Disable and Test when intent on, calls apiHookDisable and apiHookCheck", async () => {
    const d = makeData();
    d.Providers[0].HookEnabled = { PreToolUse: true };
    d.Providers[0].Hooks = { PreToolUse: { Supported: true, Verified: true, ProbedAt: "2024-01-01", Error: "", Scope: "global" } };
    vi.mocked(api.apiGetProviders).mockResolvedValue(d);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "/wick" } });
    await screen.findByText("openai/gpt4");
    fireEvent.click(screen.getByText("Disable"));
    expect(api.apiHookDisable).toHaveBeenCalledWith("/wick", "claude", "claude", "PreToolUse");
    fireEvent.click(screen.getByText("Test"));
    expect(api.apiHookCheck).toHaveBeenCalledWith("/wick", "claude", "claude", "PreToolUse");
  });

  it("hides hook action buttons when gate is locked (bypass)", async () => {
    const d = makeData();
    d.Gate = { ...d.Gate, Enabled: true, BypassLocked: true };
    vi.mocked(api.apiGetProviders).mockResolvedValue(d);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    expect(screen.queryByText("Enable")).toBeNull();
    expect(screen.getAllByText("locked (bypass)").length).toBeGreaterThan(0);
  });
});

describe("ProvidersList - active processes panel", () => {
  it("renders the panel when LiveProcesses is non-empty", async () => {
    const d = makeData();
    d.LiveProcesses = [{ SessionID: "abcdef123456", AgentName: "claude", PID: 77, Lifecycle: "working", Substate: "active" }];
    vi.mocked(api.apiGetProviders).mockResolvedValue(d);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("Active Processes");
    expect(screen.getByText("abcdef12")).toBeTruthy();
    expect(screen.getByText("77")).toBeTruthy();
  });

  it("hides the panel when LiveProcesses is empty", async () => {
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("openai/gpt4");
    expect(screen.queryByText("Active Processes")).toBeNull();
  });
});

describe("ProvidersList - wick built-in card", () => {
  function withWick(): ProvidersListResponse {
    const d = makeData();
    d.Providers.push({
      Instance: { Type: "wick", Name: "wick", Binary: "", Disabled: false, MaxConcurrent: 0, SendMode: "" },
      Path: "(built-in)",
      PathFound: true,
      CanManage: true,
      Version: "built-in",
      VersionErr: "",
      Probing: false,
      Hooks: {},
      HookEnabled: {},
      Cap: { Used: 0, Max: 0, Unlimited: true },
    });
    return d;
  }

  it("renders the Built-in badge + model count and calls onNavigate", async () => {
    vi.mocked(api.apiGetProviders).mockResolvedValue(withWick());
    vi.mocked(api.apiGetWickConfig).mockResolvedValue({
      models: [
        { ID: "m_1", Kind: "google", Label: "Gemini Flash", Model: "gemini-flash-latest", KeyMasked: "••", HasKey: true, BaseURL: "", APIFormat: "gemini", MaxOutputTokens: 0, Default: true, Disabled: false, Temperature: null, TopP: null, ThinkingBudget: null, RawConfig: "", Headers: "", LiveSet: false, DiscoveryFilter: "", DefaultVendorModel: "" },
      ],
      settings: { ShellToolDisabled: false, ShowCapabilities: true, EnableStreaming: true, CapabilityMode: "icon", Connectors: [], MaxContextTokens: 0, MaxTurns: 0, MaxConsecErrors: 0, MaxTurnMinutes: 0, MaxModelRetries: 0, ModelCallTimeoutSec: 0, Temperature: null, TopP: null, ThinkingBudget: null, RawConfig: "" },
    });
    const onNavigate = vi.fn();
    render(ProvidersList, { props: { onNavigate, onOpenSession: vi.fn(), base: "/wick" } });
    expect(await screen.findByText("Built-in")).toBeTruthy();
    expect(await screen.findByText("1 registered")).toBeTruthy();
    // The card surfaces the default model's display label.
    expect(screen.getByText("Gemini Flash")).toBeTruthy();
    // The wick card exposes a Detail button routing to wick/wick.
    const detailBtns = screen.getAllByText("Detail");
    fireEvent.click(detailBtns[detailBtns.length - 1]);
    expect(onNavigate).toHaveBeenCalledWith("wick", "wick");
  });

  it("shows Needs setup when wick has zero models", async () => {
    vi.mocked(api.apiGetProviders).mockResolvedValue(withWick());
    vi.mocked(api.apiGetWickConfig).mockResolvedValue({
      models: [],
      settings: { ShellToolDisabled: false, ShowCapabilities: true, EnableStreaming: true, CapabilityMode: "icon", Connectors: [], MaxContextTokens: 0, MaxTurns: 0, MaxConsecErrors: 0, MaxTurnMinutes: 0, MaxModelRetries: 0, ModelCallTimeoutSec: 0, Temperature: null, TopP: null, ThinkingBudget: null, RawConfig: "" },
    });
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "/wick" } });
    expect(await screen.findByText("Needs setup")).toBeTruthy();
  });
});

describe("ProvidersList connection badges", () => {
  function conn(over: Partial<ProviderConnection> = {}): ProviderConnection {
    return {
      type: "claude",
      name: "claude",
      connected: true,
      accountUnknown: false,
      email: "dev@abc.com",
      plan: "max",
      org: "",
      authMethod: "Claude AI",
      usageSupported: true,
      usageErr: "",
      usagePending: false,
      usageChecking: false,
      usageFetchedAt: "",
      usageAgeS: 0,
      usageNextS: 0,
      windows: [
        { key: "five_hour", utilization: 42, resetsAt: "" },
        { key: "seven_day", utilization: 80, resetsAt: "" },
      ],
      ...over,
    };
  }

  it("shows the account email on the matching card", async () => {
    vi.mocked(api.apiGetConnections).mockResolvedValue([conn()]);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    expect(await screen.findByText("dev@abc.com")).toBeTruthy();
  });

  it("shows both usage percentages beside the rings", async () => {
    vi.mocked(api.apiGetConnections).mockResolvedValue([conn()]);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("dev@abc.com");
    expect(screen.getByText("42%")).toBeTruthy();
    expect(screen.getByText("80%")).toBeTruthy();
  });

  it("badges an instance with no credentials as not connected", async () => {
    vi.mocked(api.apiGetConnections).mockResolvedValue([
      conn({ connected: false, email: "", plan: "", windows: [] }),
    ]);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    expect(await screen.findByText("Not connected")).toBeTruthy();
  });

  it("does not badge a card the connections payload never mentioned", async () => {
    // Only claude/claude is reported; openai/gpt4 must stay unbadged
    // rather than borrowing another instance's account.
    vi.mocked(api.apiGetConnections).mockResolvedValue([conn()]);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("dev@abc.com");
    expect(screen.getAllByText("dev@abc.com")).toHaveLength(1);
  });

  it("stamps the usage row with how old the cached reading is", async () => {
    vi.mocked(api.apiGetConnections).mockResolvedValue([
      conn({ usageAgeS: 45, usageNextS: 15, usageFetchedAt: "2026-09-12T10:00:00Z" }),
    ]);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("dev@abc.com");
    // The numbers come from a shared server-side cache, so the card must
    // say so rather than implying it fetched them on this paint.
    expect(screen.getByText("45s ago")).toBeTruthy();
    expect(screen.getByTestId("usage-cache-chip").getAttribute("title")).toContain("re-check available in 15s");
  });

  it("shows a pending reading as pending, not as an error", async () => {
    vi.mocked(api.apiGetConnections).mockResolvedValue([
      conn({ windows: [], usagePending: true }),
    ]);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    expect(await screen.findByTestId("usage-checking")).toBeTruthy();
  });

  it("says when a re-check would be accepted after a failure", async () => {
    // Retrying a 429 is what keeps it alive, so the card explains the
    // wait instead of looking stuck.
    vi.mocked(api.apiGetConnections).mockResolvedValue([
      conn({ windows: [], usageErr: "usage endpoint: 429 Too Many Requests", usageNextS: 120 }),
    ]);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    expect(await screen.findByText("re-check in 2m")).toBeTruthy();
  });

  // The button must be there even when the card HAS numbers — a reading
  // is cached for a minute, and "I want it now" is a legitimate ask.
  it("offers a re-check on a card that already has usage", async () => {
    vi.mocked(api.apiGetConnections).mockResolvedValue([conn({ usageAgeS: 30 })]);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("dev@abc.com");
    await fireEvent.click(screen.getAllByTestId("usage-recheck")[0]);
    expect(tty.apiLoginTTYUsageRefresh).toHaveBeenCalledWith("", "claude", "claude");
  });

  it("explains a refused re-check as a wait, not as a failure", async () => {
    vi.mocked(api.apiGetConnections).mockResolvedValue([conn({ usageAgeS: 30 })]);
    // The server declines because a probe now would land inside the
    // upstream's own cooldown.
    vi.mocked(tty.apiLoginTTYUsageRefresh).mockResolvedValue({ accepted: false, checking: false, waitS: 240 });
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("dev@abc.com");
    await fireEvent.click(screen.getAllByTestId("usage-recheck")[0]);
    expect(await screen.findByText("wait 4m")).toBeTruthy();
  });

  it("shows the checking state while a probe is in flight", async () => {
    vi.mocked(api.apiGetConnections).mockResolvedValue([conn({ usageAgeS: 30, usageChecking: true })]);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("dev@abc.com");
    expect(screen.getByTestId("usage-checking")).toBeTruthy();
    // While it works, the button is disabled — a second click would only
    // queue behind the same probe.
    expect((screen.getAllByTestId("usage-recheck")[0] as HTMLButtonElement).disabled).toBe(true);
  });

  // A non-admin gets a reading page: their providers and nothing that
  // would let them change the host. The API refuses these anyway — this
  // is about not offering a button that lies.
  it("hides every admin control when the caller is not an admin", async () => {
    vi.mocked(api.apiGetProviders).mockResolvedValue({ ...makeData(), IsAdmin: false });
    vi.mocked(api.apiGetConnections).mockResolvedValue([conn()]);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("claude/claude");

    expect(screen.queryByText("+ Add Custom")).toBeNull();
    expect(screen.queryByText("Rescan all")).toBeNull();
    expect(screen.queryByText(/Auto-rescan/)).toBeNull();
    expect(screen.queryByText("MCP Wick")).toBeNull();
    expect(screen.queryByText("Delete instance")).toBeNull();
    // Pool counters describe the host, not a provider.
    expect(screen.queryByText("Active Slots")).toBeNull();
    // …and the page says why it looks smaller.
    expect(screen.getByText("read-only")).toBeTruthy();
  });

  it("still lists the providers themselves for a non-admin", async () => {
    vi.mocked(api.apiGetProviders).mockResolvedValue({ ...makeData(), IsAdmin: false });
    vi.mocked(api.apiGetConnections).mockResolvedValue([conn()]);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    // The point of sharing: the card and its usage are there.
    expect(await screen.findByText("claude/claude")).toBeTruthy();
    expect(await screen.findByText("dev@abc.com")).toBeTruthy();
    expect(screen.getByText("42%")).toBeTruthy();
  });

  it("offers Re-check only on instances the caller may manage", async () => {
    const d = makeData();
    d.IsAdmin = false;
    d.Providers[0].CanManage = false;
    vi.mocked(api.apiGetProviders).mockResolvedValue(d);
    vi.mocked(api.apiGetConnections).mockResolvedValue([conn()]);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("dev@abc.com");
    // Usage is readable…
    expect(screen.getByText("42%")).toBeTruthy();
    // …but forcing a fresh probe is a manage grant this caller lacks.
    expect(screen.queryAllByTestId("usage-recheck")).toHaveLength(0);
  });

  it("offers Re-check to a non-admin who was granted manage", async () => {
    const d = makeData();
    d.IsAdmin = false;
    d.Providers[0].CanManage = true;
    vi.mocked(api.apiGetProviders).mockResolvedValue(d);
    vi.mocked(api.apiGetConnections).mockResolvedValue([conn()]);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("dev@abc.com");
    expect(screen.getAllByTestId("usage-recheck").length).toBeGreaterThan(0);
  });

  it("hides per-card Rescan from a non-admin manager", async () => {
    vi.mocked(api.apiGetProviders).mockResolvedValue({ ...makeData(), IsAdmin: false });
    vi.mocked(api.apiGetConnections).mockResolvedValue([conn()]);
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("claude/claude");
    // Rescan re-probes the host binary — admin-only, so it must not be
    // offered to someone whose click would come back 403.
    expect(screen.queryByText("Rescan")).toBeNull();
    // Detail stays: that is how a manager reaches Reconnect and usage.
    expect(screen.getAllByText("Detail").length).toBeGreaterThan(0);
  });

  it("renders cards even when the connections request fails", async () => {
    vi.mocked(api.apiGetConnections).mockRejectedValue(new Error("boom"));
    render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    // The list payload is independent — a failed usage probe must not
    // take the page down with it.
    expect(await screen.findByText("claude/claude")).toBeTruthy();
  });
});

// Recent Spawns is now its own component (RecentSpawns.svelte) with its own
// test file — ProvidersList just embeds it.

describe("ProvidersList - card rhythm", () => {
  /* The same rule the provider page broke: a wrapper that groups cards
     takes them out of the page's vertical rhythm, and they render
     flush. Asserted here too because this page is the other long stack
     of cards, and the failure is invisible to every other test. */
  it("spaces the cards it stacks", async () => {
    const { container } = render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await screen.findByText("claude/claude");
    expectCardRhythm(container);
  });
});

describe("ProvidersList managed binary indicator", () => {
  function withOmp(): ProvidersListResponse {
    const d = makeData();
    d.Providers.push({
      ...d.Providers[0],
      Instance: { ...d.Providers[0].Instance, Type: "omp", Name: "omp", Binary: "" },
      Path: "/data/providers/bin/omp/versions/18.4.3/omp",
    });
    return d;
  }
  const omp = (over: Record<string, unknown> = {}) =>
    mb.normalizeManaged({ type: "omp", enabled: true, current: "18.4.3", latest: { tag: "v18.4.4", version: "18.4.4" }, update_available: true, ...over });

  it("shows the active version + update badge, no action buttons; click opens Detail", async () => {
    vi.mocked(api.apiGetProviders).mockResolvedValue(withOmp());
    vi.mocked(mb.apiManagedList).mockResolvedValue({ types: [omp()], isAdmin: true });
    const onNavigate = vi.fn();
    render(ProvidersList, { props: { base: "", onNavigate } });
    const ind = await screen.findByTestId("card-managed-binary");
    expect(ind.dataset.state).toBe("installed");
    expect(ind.textContent).toContain("v18.4.3");
    expect(screen.getByTestId("card-managed-update").textContent).toBe("update available v18.4.4");
    expect(screen.queryByTestId("managed-binary-panel")).toBeNull();
    await fireEvent.click(ind);
    expect(onNavigate).toHaveBeenCalledWith("omp", "omp");
  });

  it("not installed / running download", async () => {
    vi.mocked(api.apiGetProviders).mockResolvedValue(withOmp());
    vi.mocked(mb.apiManagedList).mockResolvedValue({
      types: [omp({ current: "", update_available: false, job: { phase: "download", done: 45, total: 100, tag: "v18.4.4", version: "18.4.4" } })],
      isAdmin: true,
    });
    render(ProvidersList, { props: { base: "", onNavigate: vi.fn() } });
    const ind = await screen.findByTestId("card-managed-binary");
    expect(ind.dataset.state).toBe("missing");
    expect(screen.getByTestId("card-managed-job").textContent).toBe("Downloading v18.4.4… 45%");
    expect(screen.queryByTestId("card-managed-update")).toBeNull();
  });
});

describe("ProvidersList card header on narrow screens", () => {
  function withIsolated(): ProvidersListResponse {
    const d = makeData();
    for (const type of ["omp", "opencode"]) {
      d.Providers.push({
        ...d.Providers[0],
        Instance: { ...d.Providers[0].Instance, Type: type, Name: "a-rather-long-instance-name", Binary: "" },
        Cap: { Used: 0, Max: 20, Unlimited: false },
      });
    }
    return d;
  }

  it("clamps the name to two wrapped lines with the full name in title, with the cap on one line beside it", async () => {
    vi.mocked(api.apiGetProviders).mockResolvedValue(withIsolated());
    render(ProvidersList, { props: { base: "", onNavigate: vi.fn() } });
    await screen.findByText("omp/a-rather-long-instance-name");
    const name = screen.getAllByTestId("card-name").find((n) => n.textContent === "omp/a-rather-long-instance-name")!;
    expect(name.className).toContain("line-clamp-2");
    expect(name.className).toContain("break-all");
    expect(name.className).toContain("min-w-0");
    expect(name.getAttribute("title")).toBe("omp/a-rather-long-instance-name");
    const titleCol = name.parentElement!.parentElement!;
    expect(titleCol.className).toContain("min-w-0");
    expect(titleCol.className).toContain("flex-1");
    // The actions never wrap under the title: the header row does not wrap
    // and the button group does not shrink.
    const header = titleCol.parentElement!;
    expect(header.className).not.toContain("flex-wrap");
    expect((titleCol.nextElementSibling as HTMLElement).className).toContain("shrink-0");
    // Cap + info icon ride right after the name, in a group that never
    // shrinks, so a long name clamps beside them instead of pushing them.
    const cap = titleCol.querySelector('[data-testid="card-cap"]')!;
    const group = cap.parentElement!;
    expect(group.contains(titleCol.querySelector('[data-testid="one-account-badge"]'))).toBe(true);
    expect(group.contains(name)).toBe(false);
    expect(group.parentElement).toBe(name.parentElement);
    expect(group.className).toContain("shrink-0");
    for (const cap of screen.getAllByTestId("card-cap")) {
      expect(cap.className).toContain("whitespace-nowrap");
    }
  });

  it("shows a login & usage placeholder on every credentialed card until the connections land", async () => {
    let resolve!: (v: Awaited<ReturnType<typeof api.apiGetConnections>>) => void;
    vi.mocked(api.apiGetConnections).mockReturnValue(new Promise((r) => (resolve = r)));
    render(ProvidersList, { props: { base: "", onNavigate: vi.fn() } });
    await screen.findAllByTestId("card-name");
    expect(screen.getAllByTestId("conn-loading").length).toBeGreaterThan(0);
    resolve([]);
    await waitFor(() => expect(screen.queryAllByTestId("conn-loading").length).toBe(0));
  });

  it("says 'checking login' until the connections land, and while the login is unreadable", async () => {
    vi.mocked(api.apiGetProviders).mockResolvedValue(withIsolated());
    let resolve!: (v: Awaited<ReturnType<typeof api.apiGetConnections>>) => void;
    vi.mocked(api.apiGetConnections).mockReturnValue(new Promise((r) => (resolve = r)));
    render(ProvidersList, { props: { base: "", onNavigate: vi.fn() } });
    await screen.findByText("omp/a-rather-long-instance-name");
    expect(screen.getAllByTestId("card-account-loading").length).toBe(2);
    expect(screen.queryByText(/not logged in/)).toBeNull();
    resolve([
      { type: "omp", name: "a-rather-long-instance-name", connected: false, accountUnknown: true, email: "", plan: "", org: "", authMethod: "", usageSupported: true, usageErr: "", usagePending: true, usageChecking: false, usageFetchedAt: "", usageAgeS: 0, usageNextS: 0, windows: [] },
    ]);
    // omp could not be read → still checking; opencode has no row → logged out.
    await waitFor(() => expect(screen.getAllByTestId("card-account-loading").length).toBe(1));
    expect(screen.getByTestId("conn-checking")).toBeTruthy();
    expect(screen.getByText(/not logged in/)).toBeTruthy();
  });

  it("replaces the one-account text badge with an info icon that explains the type", async () => {
    vi.mocked(api.apiGetProviders).mockResolvedValue(withIsolated());
    render(ProvidersList, { props: { base: "", onNavigate: vi.fn() } });
    await screen.findByText("omp/a-rather-long-instance-name");
    const [omp, opencode] = screen.getAllByTestId("one-account-badge");
    expect(omp.querySelector('svg[data-icon="info"] circle')).not.toBeNull();
    expect(omp.getAttribute("title")).toBe("One instance = one omp profile. It can hold several accounts; omp rotates between them.");
    expect(omp.getAttribute("aria-label")).toBe(omp.getAttribute("title"));
    expect(opencode.getAttribute("title")).toBe("One instance = one data folder. Add a second account of a provider as an extra account folder.");
    expect(screen.queryByText("1 instance = 1 account")).toBeNull();

    // A tap opens the popover (touch screens never show a title) and a second tap closes it.
    await fireEvent.click(omp);
    expect(screen.getByTestId("one-account-badge-popover").textContent).toContain("omp rotates between them");
    expect(omp.getAttribute("aria-expanded")).toBe("true");
    await fireEvent.click(omp);
    expect(screen.queryByTestId("one-account-badge-popover")).toBeNull();
  });
});

describe("ProvidersList unmount", () => {
  it("does not keep following a download after unmount", async () => {
    let resolve!: (v: { types: mb.ManagedBinary[]; isAdmin: boolean }) => void;
    vi.mocked(mb.apiManagedList).mockReturnValueOnce(new Promise((r) => { resolve = r; }));
    const { unmount } = render(ProvidersList, { props: { onNavigate: vi.fn(), onOpenSession: vi.fn(), base: "" } });
    await waitFor(() => expect(mb.apiManagedList).toHaveBeenCalled());
    unmount();
    const spy = vi.spyOn(globalThis, "setTimeout");
    const running = mb.normalizeManaged({
      type: "omp", enabled: true,
      job: { id: "j", type: "omp", tag: "v1", version: "1", activate: false, phase: "download" },
    } as Parameters<typeof mb.normalizeManaged>[0]);
    resolve({ types: [running], isAdmin: true });
    await new Promise((r) => queueMicrotask(() => r(undefined)));
    await Promise.resolve();
    expect(spy.mock.calls.filter((c) => c[1] === 2000)).toHaveLength(0);
    spy.mockRestore();
  });
});
