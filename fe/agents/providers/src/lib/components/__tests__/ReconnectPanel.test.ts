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
    apiLoginTTYLogout: vi.fn(),
    apiSetAPIKey: vi.fn(),
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
    loginChoices: [],
    loginNote: "",
    accountStore: "",
    accounts: [],
    apiKeys: [],
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
    expect(logintty.apiLoginTTYStart).toHaveBeenCalledWith("/tools/agents", "claude", "main", "");
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

  it("keeps asking while a probe is in flight, then shows the finished reading", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(makeStatus());
    vi.mocked(logintty.apiLoginTTYUsage)
      .mockResolvedValueOnce(makeUsage({ checking: true }))
      .mockResolvedValue(makeUsage({ checking: false }));
    render(ReconnectPanel, { props: { base: "/tools/agents", type: "claude", name: "main" } });
    await screen.findByText("dev@abc.com");
    await fireEvent.click(screen.getByText("Connection"));
    expect(await screen.findByTestId("panel-usage-checking")).toBeTruthy();
    // The panel polls on its own; no reload, no second click.
    await vi.waitFor(() => expect(screen.queryByTestId("panel-usage-checking")).toBeNull(), { timeout: 5000 });
    expect(vi.mocked(logintty.apiLoginTTYUsage).mock.calls.length).toBeGreaterThanOrEqual(2);
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

  it("omp: not connected opens the login picker, warns for anthropic, passes the choice to start", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(
      makeStatus({
        account: { connected: false, email: "", plan: "", org: "", authMethod: "", expiresAt: "" },
        loginChoices: [
          { id: "openai-codex-device", label: "ChatGPT device", warning: "", default: true },
          { id: "anthropic", label: "Claude", warning: "policy risk", default: false },
        ],
        loginNote: "one account per instance",
        accountStore: "profile wick-omp",
      }),
    );
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage({ supported: false, windows: [] }));
    vi.mocked(logintty.apiLoginTTYStart).mockResolvedValue(null);
    render(ReconnectPanel, { props: { base: "", type: "omp", name: "omp" } });
    const picker = await screen.findByTestId("panel-login-choice");
    expect(picker).toBeTruthy();
    expect(screen.getByTestId("panel-account-store").textContent).toContain("wick-omp");
    expect(screen.queryByTestId("panel-login-warning")).toBeNull();
    await fireEvent.click(picker.querySelector('[data-testid="wick-select-trigger"]') as HTMLElement);
    await fireEvent.click(document.body.querySelector('[role="option"][data-value="anthropic"]') as HTMLElement);
    expect((await screen.findByTestId("panel-login-warning")).textContent).toContain("policy risk");
    await fireEvent.click(screen.getByText("Reconnect"));
    expect(vi.mocked(logintty.apiLoginTTYStart)).toHaveBeenCalledWith("", "omp", "omp", "anthropic");
  });

  it("omp: lists every pooled account, adds another, logs a provider out", async () => {
    const acct = (id: string, label: string, status = "active") => ({
      id, label, provider: "openai-codex", email: label, plan: "plus", org: "", kind: "oauth",
      status, disabledCause: status === "disabled" ? "invalid_grant" : "", disabledAt: "", usage: [],
    });
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(
      makeStatus({
        account: { connected: true, email: "a@x.test", plan: "plus", org: "", authMethod: "openai-codex", expiresAt: "0001-01-01T00:00:00Z" },
        loginChoices: [{ id: "openai-codex-device", label: "ChatGPT device", warning: "", default: true, beta: false }],
        accountStore: "profile wick-omp",
        accounts: [acct("openai-codex#1", "a@x.test"), acct("openai-codex#2", "b@x.test", "disabled")],
      }),
    );
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage({ supported: false, windows: [] }));
    vi.mocked(logintty.apiLoginTTYStart).mockResolvedValue(null);
    vi.mocked(logintty.apiLoginTTYLogout).mockResolvedValue(undefined);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    render(ReconnectPanel, { props: { base: "", type: "omp", name: "omp", defaultExpanded: true } });
    expect((await screen.findAllByTestId("panel-account-row")).length).toBe(2);
    expect(screen.getByText("disabled")).toBeTruthy();
    expect(screen.getByTestId("panel-multi-account-note")).toBeTruthy();
    // Go's zero time never renders as "Token expires 1/1/1".
    expect(screen.queryByText(/Token expires/)).toBeNull();
    await fireEvent.click(screen.getByTestId("panel-add-account"));
    expect(vi.mocked(logintty.apiLoginTTYStart)).toHaveBeenCalledWith("", "omp", "omp", "openai-codex-device");
    await fireEvent.click(screen.getByTestId("panel-logout-openai-codex"));
    expect(vi.mocked(logintty.apiLoginTTYLogout)).toHaveBeenCalledWith("", "omp", "omp", "openai-codex", "");
  });

  it("API key: marks providers that have a key and saves a new one", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(
      makeStatus({
        apiKeys: [
          { id: "openrouter", label: "OpenRouter", env: "OPENROUTER_API_KEY", set: true },
          { id: "groq", label: "Groq", env: "GROQ_API_KEY", set: false },
        ],
      }),
    );
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage({ supported: false, windows: [] }));
    vi.mocked(logintty.apiSetAPIKey).mockResolvedValue(undefined);
    render(ReconnectPanel, { props: { base: "", type: "opencode", name: "oc", defaultExpanded: true } });
    const box = await screen.findByTestId("panel-api-key");
    expect(screen.getByTestId("panel-api-keys-set").textContent).toContain("OpenRouter");
    await fireEvent.click(box.querySelector('[data-testid="wick-select-trigger"]') as HTMLElement);
    await fireEvent.click(document.body.querySelector('[role="option"][data-value="groq"]') as HTMLElement);
    await fireEvent.input(await screen.findByTestId("panel-api-key-input"), { target: { value: "k-test" } });
    await fireEvent.click(screen.getByTestId("panel-api-key-save"));
    expect(vi.mocked(logintty.apiSetAPIKey)).toHaveBeenCalledWith("", "opencode", "oc", "groq", "k-test");
  });

  it("opencode: lists logged-in providers per account folder and removes one", async () => {
    const row = (id: string, kind: string) => ({ id, label: "Account " + id.split("/")[0], provider: id.split("/")[1], email: "", plan: "", org: "", kind, status: "active", disabledCause: "", disabledAt: "" });
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(
      makeStatus({ accountStore: "data dir x", accounts: [row("main/openai", "oauth"), row("main/openrouter", "api"), row("a2/openai", "oauth")] }),
    );
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage({ supported: false, windows: [] }));
    vi.mocked(logintty.apiLoginTTYLogout).mockResolvedValue(undefined);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    render(ReconnectPanel, { props: { base: "", type: "opencode", name: "oc", defaultExpanded: true } });
    expect((await screen.findAllByTestId("panel-account-row")).length).toBe(3);
    expect(screen.getByText("(API key)")).toBeTruthy();
    expect(screen.getByTestId("panel-multi-provider-note").textContent).toContain("separate instance");
    // Removal is per account folder: the a2 login, not main's.
    expect(screen.queryByTestId("panel-logout-openai")).toBeNull();
    await fireEvent.click(screen.getByTestId("panel-logout-a2/openai"));
    expect(vi.mocked(logintty.apiLoginTTYLogout)).toHaveBeenCalledWith("", "opencode", "oc", "openai", "a2");
  });

  it("opencode: a second account of a provider logs in to a new data folder", async () => {
    const row = (id: string) => ({ id, label: id, provider: id, email: "", plan: "", org: "", kind: "oauth", status: "active", disabledCause: "", disabledAt: "" });
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(makeStatus({ accountStore: "data dir x", accounts: [row("openai")] }));
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage({ supported: false, windows: [] }));
    vi.mocked(logintty.apiLoginTTYStart).mockResolvedValue(null);
    render(ReconnectPanel, { props: { base: "", type: "opencode", name: "oc", defaultExpanded: true } });
    await fireEvent.click(await screen.findByTestId("panel-add-account-folder"));
    const call = vi.mocked(logintty.apiLoginTTYStart).mock.calls.at(-1)!;
    expect([call[0], call[1], call[2], call[4]]).toEqual(["", "opencode", "oc", "new"]);
  });

  it("shared login: names the owner with a link and hides login controls", async () => {
    vi.mocked(logintty.apiLoginTTYStatus).mockResolvedValue(
      makeStatus({
        authFrom: "oc-main",
        accountStore: "/data/oc",
        accounts: [{ id: "openai", provider: "openai", label: "openai", email: "", plan: "", active: true, disabled: false } as never],
        loginChoices: [{ id: "openai", label: "OpenAI", warning: "", default: true, beta: false } as never],
        apiKeys: [{ id: "anthropic", label: "Anthropic", env: "ANTHROPIC_API_KEY", set: false }],
      }),
    );
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage());
    render(ReconnectPanel, { props: { base: "/tools/agents", type: "opencode", name: "oc2", defaultExpanded: true } });
    const note = await screen.findByTestId("panel-shared-login");
    expect(note.textContent).toContain("Uses the login of");
    expect(screen.getByTestId("panel-shared-login-owner").getAttribute("href")).toBe("/tools/agents/opencode/oc-main");
    expect(screen.getByTestId("panel-shared-login-race")).toBeTruthy();
    expect(screen.queryByTestId("panel-login-choice")).toBeNull();
    expect(screen.queryByTestId("panel-api-key")).toBeNull();
    expect(screen.queryByTestId("panel-add-account-folder")).toBeNull();
    expect(screen.queryByRole("button", { name: /reconnect/i })).toBeNull();
    // Usage (the owner's) still shows.
    expect(screen.getByText("Session (5hr)")).toBeTruthy();
  });
});

describe("ReconnectPanel unmount", () => {
  it("does not re-arm the unknown-login retry after unmount", async () => {
    let resolve!: (v: LoginTTYStatus) => void;
    vi.mocked(logintty.apiLoginTTYStatus).mockReturnValue(new Promise((r) => { resolve = r; }));
    vi.mocked(logintty.apiLoginTTYUsage).mockResolvedValue(makeUsage());
    const { unmount } = render(ReconnectPanel, { props: { base: "/tools/agents", type: "claude", name: "main" } });
    unmount();
    const spy = vi.spyOn(globalThis, "setTimeout");
    const st = makeStatus();
    resolve({ ...st, account: { ...st.account, unknown: true } });
    await new Promise((r) => queueMicrotask(() => r(undefined)));
    await Promise.resolve();
    expect(spy.mock.calls.filter((c) => c[1] === 5000)).toHaveLength(0);
    spy.mockRestore();
  });
});
