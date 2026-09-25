import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import AgentMemoryConfig from "../AgentMemoryConfig.svelte";
import * as api from "$lib/api.js";

vi.mock("$lib/api.js");
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: vi.fn(),
  toasts: { subscribe: vi.fn(() => vi.fn()) },
}));

type Props = Parameters<typeof AgentMemoryConfig>[1];

function props(over: Partial<Props> = {}): Props {
  return {
    base: "/b",
    type: "claude",
    supported: true,
    featureEnabled: true,
    useAgentMemory: false,
    provider: "ai-memory",
    backends: [{ ID: "ai-memory", Name: "ai-memory", Blurb: "local memory server", GitHubURL: "https://github.com/x/ai-memory" }],
    serverUrl: "",
    effectiveUrl: "http://127.0.0.1:49374",
    authKey: "",
    authKeyMasked: false,
    capture: false,
    captureSupported: true,
    captureNote: "",
    configPreview: "",
    ...over,
  } as Props;
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.apiAgentMemoryTest).mockResolvedValue({ ok: true, baseUrl: "http://127.0.0.1:49374", error: "", version: "2.4.0" });
  vi.mocked(api.apiAgentMemoryStart).mockResolvedValue(undefined);
});

describe("AgentMemoryConfig - visibility", () => {
  it("renders nothing for a provider type the backend cannot wire", () => {
    // A control that has no effect is worse than an absent one: it tells
    // the operator they configured something when they did not.
    const { container } = render(AgentMemoryConfig, { props: props({ supported: false }) });
    expect(container.textContent).toBe("");
  });

  it("disables the switch while the feature's master switch is off", () => {
    render(AgentMemoryConfig, { props: props({ featureEnabled: false }) });
    expect((screen.getByLabelText("Use agent memory") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/switched off for this server/i)).toBeTruthy();
  });
});

describe("AgentMemoryConfig - preflight (PLAN §18.4)", () => {
  it("leaves the switch OFF when the daemon does not answer", async () => {
    vi.mocked(api.apiAgentMemoryTest).mockResolvedValue({
      ok: false, baseUrl: "http://127.0.0.1:49374", error: "no 200 from http://127.0.0.1:49374/healthz", version: "",
    });
    render(AgentMemoryConfig, { props: props() });
    await fireEvent.click(screen.getByLabelText("Use agent memory"));
    await waitFor(() => expect(screen.getByText(/did not answer/i)).toBeTruthy());
    // The whole point: nothing was silently enabled.
    expect(screen.getByLabelText("Use agent memory").getAttribute("aria-checked")).toBe("false");
    expect(screen.getByText("Start daemon")).toBeTruthy();
    expect(screen.getByText("Turn it on anyway")).toBeTruthy();
  });

  it("turns on when the daemon answers", async () => {
    render(AgentMemoryConfig, { props: props() });
    await fireEvent.click(screen.getByLabelText("Use agent memory"));
    await waitFor(() => expect(screen.getByLabelText("Use agent memory").getAttribute("aria-checked")).toBe("true"));
    expect(api.apiAgentMemoryTest).toHaveBeenCalledWith("/b", "ai-memory");
  });

  it("lets the operator override a failed probe", async () => {
    vi.mocked(api.apiAgentMemoryTest).mockResolvedValue({ ok: false, baseUrl: "", error: "connection refused", version: "" });
    render(AgentMemoryConfig, { props: props() });
    await fireEvent.click(screen.getByLabelText("Use agent memory"));
    await waitFor(() => expect(screen.getByText("Turn it on anyway")).toBeTruthy());
    await fireEvent.click(screen.getByText("Turn it on anyway"));
    await waitFor(() => expect(screen.getByLabelText("Use agent memory").getAttribute("aria-checked")).toBe("true"));
    // The warning is NOT cleared — the choice is recorded, not validated.
    expect(screen.getByText(/did not answer/i)).toBeTruthy();
  });

  it("skips the probe for an instance pointed at an unmanaged server", async () => {
    // /test probes the daemon WICK manages, which says nothing about a
    // remote endpoint — reporting it as proof would be a lie.
    render(AgentMemoryConfig, { props: props({ serverUrl: "http://10.0.0.5:49374" }) });
    await fireEvent.click(screen.getByLabelText("Use agent memory"));
    await waitFor(() => expect(screen.getByLabelText("Use agent memory").getAttribute("aria-checked")).toBe("true"));
    expect(api.apiAgentMemoryTest).not.toHaveBeenCalled();
    expect(screen.getByText(/wick does not manage/i)).toBeTruthy();
  });

  it("does not probe when switching OFF", async () => {
    render(AgentMemoryConfig, { props: props({ useAgentMemory: true }) });
    await fireEvent.click(screen.getByLabelText("Use agent memory"));
    await waitFor(() => expect(screen.getByLabelText("Use agent memory").getAttribute("aria-checked")).toBe("false"));
    expect(api.apiAgentMemoryTest).not.toHaveBeenCalled();
  });

  it("enables the instance after Start daemon brings it up", async () => {
    vi.mocked(api.apiAgentMemoryTest)
      .mockResolvedValueOnce({ ok: false, baseUrl: "", error: "connection refused", version: "" })
      .mockResolvedValueOnce({ ok: true, baseUrl: "http://127.0.0.1:49374", error: "", version: "2.4.0" });
    render(AgentMemoryConfig, { props: props() });
    await fireEvent.click(screen.getByLabelText("Use agent memory"));
    await waitFor(() => expect(screen.getByText("Start daemon")).toBeTruthy());
    await fireEvent.click(screen.getByText("Start daemon"));
    await waitFor(() => expect(screen.getByLabelText("Use agent memory").getAttribute("aria-checked")).toBe("true"));
    expect(api.apiAgentMemoryStart).toHaveBeenCalledWith("/b", "ai-memory");
  });
});

describe("AgentMemoryConfig - capture", () => {
  it("names the cost of recording when it works", async () => {
    render(AgentMemoryConfig, { props: props({ useAgentMemory: true }) });
    expect((screen.getByLabelText("Record this instance's sessions") as HTMLButtonElement).disabled).toBe(false);
    expect(screen.getByText(/PreToolUse and PostToolUse/)).toBeTruthy();
  });

  it("disables recording and says why when it would do nothing", async () => {
    render(AgentMemoryConfig, {
      props: props({
        type: "codex",
        useAgentMemory: true,
        captureSupported: false,
        captureNote: "Recording is not wired for codex right now — this instance can read memory, but its sessions are not recorded.",
      }),
    });
    expect((screen.getByLabelText("Record this instance's sessions") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/not wired for codex/i)).toBeTruthy();
  });

  it("shows the backend's upstream repo so the operator can see what runs", () => {
    render(AgentMemoryConfig, { props: props({ useAgentMemory: true }) });
    const link = screen.getByText(/on GitHub/).closest("a");
    expect(link?.getAttribute("href")).toBe("https://github.com/x/ai-memory");
  });

  it("never prefills a stored token, only hints that one exists", () => {
    render(AgentMemoryConfig, { props: props({ useAgentMemory: true, authKeyMasked: true }) });
    const field = screen.getByLabelText(/Auth token/) as HTMLInputElement;
    expect(field.value).toBe("");
    expect(field.getAttribute("type")).toBe("password");
    expect(field.getAttribute("placeholder")).toContain("leave empty to keep");
  });
});
