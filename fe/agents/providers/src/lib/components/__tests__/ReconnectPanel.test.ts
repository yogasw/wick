import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ReconnectPanel from "../ReconnectPanel.svelte";
import * as logintty from "$lib/logintty.js";
import type { LoginTTYStatus, UsageResult } from "$lib/logintty.js";

vi.mock("$lib/logintty.js", async (importOriginal) => {
  const orig = await importOriginal<typeof import("$lib/logintty.js")>();
  return {
    ...orig,
    apiLoginTTYStatus: vi.fn(),
    apiLoginTTYUsage: vi.fn(),
    apiLoginTTYStart: vi.fn(),
    apiLoginTTYUsageRefresh: vi.fn(),
  };
});
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: vi.fn(),
  toasts: { subscribe: vi.fn(() => vi.fn()) },
}));

function makeStatus(over: Partial<LoginTTYStatus> = {}): LoginTTYStatus {
  return {
    supported: true,
    account: { connected: true, email: "dev@abc.com", plan: "team", org: "abc org", authMethod: "Claude AI", expiresAt: "" },
    session: null,
    defaultTtlS: 300,
    extendS: 300,
    maxTtlS: 1800,
    ...over,
  };
}

function makeUsage(over: Partial<UsageResult> = {}): UsageResult {
  return {
    supported: true,
    windows: [
      { key: "five_hour", utilization: 42.5, resetsAt: "2030-01-01T00:00:00Z" },
      { key: "seven_day", utilization: 80, resetsAt: "" },
    ],
    error: "",
    pending: false,
    checking: false,
    fetchedAt: "",
    ageS: 0,
    nextS: 0,
    ...over,
  };
}

beforeEach(() => {
  vi.resetAllMocks();
});

const notConnected = () =>
  makeStatus({ account: { connected: false, email: "", plan: "", org: "", authMethod: "", expiresAt: "" } });

describe("ReconnectPanel", () => {
  it("collapsed by default: connected shows only the summary badge", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(makeStatus());
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage());
    render(ReconnectPanel, { props: { base: "/tools/agents", type: "claude", name: "main" } });
    expect(await screen.findByText("Connected")).toBeTruthy();
    expect(screen.queryByText("Auth method")).toBeNull();
    expect(screen.queryByText("Session (5hr)")).toBeNull();
    // Connected → no Reconnect button while collapsed.
    expect(screen.queryByRole("button", { name: /reconnect/i })).toBeNull();
  });

  it("collapsed + not connected: Reconnect button appears in the summary", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(notConnected());
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage({ windows: [], error: "no creds" }));
    render(ReconnectPanel, { props: { base: "/tools/agents", type: "claude", name: "main" } });
    expect(await screen.findByText("Not connected")).toBeTruthy();
    expect(screen.getByRole("button", { name: /reconnect/i })).toBeTruthy();
  });

  it("expand reveals account rows and usage; collapse hides them again", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(makeStatus());
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage());
    render(ReconnectPanel, { props: { base: "/tools/agents", type: "claude", name: "main" } });
    await screen.findByText("Connected");
    await fireEvent.click(screen.getByText("Connection"));
    expect(await screen.findByText("Auth method")).toBeTruthy();
    expect(screen.getByText("Claude AI")).toBeTruthy();
    expect(screen.getByText("abc org")).toBeTruthy();
    expect(screen.getByText("Claude team")).toBeTruthy();
    expect(screen.getByText("Session (5hr)")).toBeTruthy();
    expect(screen.getByText("43%")).toBeTruthy(); // 42.5 rounded
    expect(screen.getAllByText(/Resets in /).length).toBeGreaterThan(0);
    await fireEvent.click(screen.getByText("Connection"));
    expect(screen.queryByText("Auth method")).toBeNull();
  });

  it("clicking the header row toggles the details", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(makeStatus());
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage());
    render(ReconnectPanel, { props: { base: "/tools/agents", type: "claude", name: "main" } });
    await screen.findByText("Connected");
    await fireEvent.click(screen.getByText("Connection"));
    expect(await screen.findByText("Auth method")).toBeTruthy();
    await fireEvent.click(screen.getByText("Connection"));
    expect(screen.queryByText("Auth method")).toBeNull();
  });

  it("clicking Reconnect in the header does not toggle the details", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(notConnected());
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage({ windows: [] }));
    vi.mocked(logintty.apiLoginTTYStart).mockResolvedValue(null);
    render(ReconnectPanel, { props: { base: "/tools/agents", type: "claude", name: "main" } });
    const btn = await screen.findByRole("button", { name: /reconnect/i });
    await fireEvent.click(btn);
    expect(screen.queryByText("Auth method")).toBeNull();
  });

  it("starts a login session on Reconnect click", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(notConnected());
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage({ windows: [] }));
    vi.mocked(logintty.apiLoginTTYStart).mockResolvedValue({ id: "s1", state: "running", remainingS: 300, capS: 1800 });
    render(ReconnectPanel, { props: { base: "/tools/agents", type: "claude", name: "main" } });
    const btn = await screen.findByRole("button", { name: /reconnect/i });
    await fireEvent.click(btn);
    expect(logintty.apiLoginTTYStart).toHaveBeenCalledWith("/tools/agents", "claude", "main");
  });

  it("no Reconnect button for unsupported types even when disconnected", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(
      makeStatus({ supported: false, account: { connected: false, email: "", plan: "", org: "", authMethod: "", expiresAt: "" } }),
    );
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage({ supported: false, windows: [] }));
    render(ReconnectPanel, { props: { base: "/tools/agents", type: "codex", name: "x" } });
    expect(await screen.findByText("Not connected")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /reconnect/i })).toBeNull();
    await fireEvent.click(screen.getByText("Connection"));
    expect(await screen.findByText(/not available/i)).toBeTruthy();
  });

  it("shows how old the cached usage reading is when expanded", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(makeStatus());
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(
      makeUsage({ ageS: 90, nextS: 30, fetchedAt: "2026-09-12T10:00:00Z" }),
    );
    render(ReconnectPanel, { props: { base: "/tools/agents", type: "claude", name: "main" } });
    await screen.findByText("dev@abc.com");
    await fireEvent.click(screen.getByText("Connection"));
    // The panel reads a cache shared with the providers list; opening it
    // must not imply it went and fetched fresh numbers.
    expect(await screen.findByText("2m ago")).toBeTruthy();
  });

  it("shows a queued first probe as pending rather than as a failure", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(makeStatus());
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage({ windows: [], pending: true }));
    render(ReconnectPanel, { props: { base: "/tools/agents", type: "claude", name: "main" } });
    await screen.findByText("dev@abc.com");
    await fireEvent.click(screen.getByText("Connection"));
    expect(await screen.findByText(/Checking usage/i)).toBeTruthy();
  });

  it("offers a re-check next to the usage bars and sends it", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(makeStatus());
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage({ ageS: 20 }));
    vi.mocked(logintty.apiLoginTTYUsageRefresh).mockResolvedValue({ accepted: true, checking: true, waitS: 0 });
    render(ReconnectPanel, { props: { base: "/tools/agents", type: "claude", name: "main" } });
    await screen.findByText("dev@abc.com");
    await fireEvent.click(screen.getByText("Connection"));
    await fireEvent.click(await screen.findByTestId("panel-usage-recheck"));
    expect(logintty.apiLoginTTYUsageRefresh).toHaveBeenCalledWith("/tools/agents", "claude", "main");
  });

  it("shows the wait when the server declines a re-check", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(makeStatus());
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage({ ageS: 20 }));
    vi.mocked(logintty.apiLoginTTYUsageRefresh).mockResolvedValue({ accepted: false, checking: false, waitS: 90 });
    render(ReconnectPanel, { props: { base: "/tools/agents", type: "claude", name: "main" } });
    await screen.findByText("dev@abc.com");
    await fireEvent.click(screen.getByText("Connection"));
    await fireEvent.click(await screen.findByTestId("panel-usage-recheck"));
    expect(await screen.findByText("wait 2m")).toBeTruthy();
  });
});
