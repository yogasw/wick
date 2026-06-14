import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ProviderDetail from "../ProviderDetail.svelte";
import * as api from "$lib/api.js";
import type { ProviderDetailResponse } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: vi.fn(),
  toasts: { subscribe: vi.fn(() => vi.fn()) },
}));

function makeDetail(): ProviderDetailResponse {
  return {
    Instance: { Type: "claude", Name: "default", Binary: "claude", Disabled: false, MaxConcurrent: 4, SendMode: "" },
    Path: "/usr/bin/claude",
    PathFound: true,
    Version: "1.2.3",
    VersionErr: "",
    Probing: false,
    Hooks: {
      pre_tool_use: { Supported: true, Verified: true, ProbedAt: "2024-01-01T00:00:00Z", Error: "", Scope: "global" },
    },
    HookEnabled: { pre_tool_use: false },
    Gate: { Enabled: true, Binary: "/usr/bin/gate", Source: "config", Reason: "", Note: "auto mode", PermissionMode: "default", BypassLocked: false },
    GlobalMax: 8,
    ActiveCount: 0,
    ActivePIDs: [],
    ConfigFields: [
      { Key: "api_key", Value: "••••••••", Type: "text", Options: "", IsSecret: true, Description: "API Key", Required: true },
      { Key: "model", Value: "claude-3", Type: "text", Options: "", IsSecret: false, Description: "Model name", Required: false },
      { Key: "mode", Value: "auto", Type: "select", Options: "auto,manual,off", IsSecret: false, Description: "Mode", Required: false },
    ],
    Spawns: [
      { Path: "/tmp/s.log", ProviderType: "claude", ProviderName: "default", SessionID: "sess-abc-1234", StartedAt: "2024-01-01T00:00:00Z", PID: 42, Origin: "web", FirstUserMessage: "Hello", Binary: "claude", ExitReason: "done" },
    ],
    Page: 1,
    HasNext: false,
  };
}

const defaultProps = { base: "", type: "claude", name: "default", onBack: vi.fn() };

beforeEach(() => {
  vi.mocked(api.apiGetProviderDetail).mockResolvedValue(makeDetail());
  vi.mocked(api.apiSaveProviderDetail).mockResolvedValue(undefined);
  vi.mocked(api.apiSaveConfigKey).mockResolvedValue(undefined);
  vi.mocked(api.apiHookCheck).mockResolvedValue(undefined);
  vi.mocked(api.apiHookEnable).mockResolvedValue(undefined);
  vi.mocked(api.apiHookDisable).mockResolvedValue(undefined);
  vi.mocked(api.apiDeleteProvider).mockResolvedValue(undefined);
  vi.mocked(api.apiProbeGate).mockResolvedValue(undefined);
});

describe("ProviderDetail - rendering", () => {
  it("renders provider heading with type/name", async () => {
    render(ProviderDetail, { props: defaultProps });
    expect(await screen.findByText("claude/default")).toBeTruthy();
  });

  it("renders version badge when path found", async () => {
    render(ProviderDetail, { props: defaultProps });
    expect(await screen.findByText("1.2.3")).toBeTruthy();
  });

  it("renders resolved path in binary info", async () => {
    render(ProviderDetail, { props: defaultProps });
    expect(await screen.findByText("/usr/bin/claude")).toBeTruthy();
  });

  it("renders config fields", async () => {
    render(ProviderDetail, { props: defaultProps });
    expect(await screen.findByText("api_key")).toBeTruthy();
    expect(screen.getByText("model")).toBeTruthy();
    /* "mode" appears in config key label and gate section — use getAllByText */
    expect(screen.getAllByText("mode").length).toBeGreaterThan(0);
  });

  it("renders secret field as password input", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("api_key");
    const inputs = document.querySelectorAll("input[type=password]");
    expect(inputs.length).toBeGreaterThan(0);
  });

  it("renders select input for select-type fields", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("api_key");
    const selects = document.querySelectorAll("select");
    expect(selects.length).toBeGreaterThan(0);
  });

  it("renders required marker for required fields", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("api_key");
    const markers = document.querySelectorAll("[title=required]");
    expect(markers.length).toBeGreaterThan(0);
  });

  it("renders hooks section", async () => {
    render(ProviderDetail, { props: defaultProps });
    expect(await screen.findByText("Hooks")).toBeTruthy();
    expect(screen.getByText("pre_tool_use")).toBeTruthy();
  });

  it("renders hook verified badge", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("pre_tool_use");
    expect(screen.getByText("verified")).toBeTruthy();
  });

  it("renders Enable button when hook is not enabled", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("pre_tool_use");
    expect(screen.getByText("Enable")).toBeTruthy();
  });

  it("renders Disable button when hook is enabled", async () => {
    const data = makeDetail();
    data.HookEnabled["pre_tool_use"] = true;
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(data);
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("pre_tool_use");
    expect(screen.getByText("Disable")).toBeTruthy();
  });

  it("renders gate section", async () => {
    render(ProviderDetail, { props: defaultProps });
    expect(await screen.findByText("Command Gate")).toBeTruthy();
    expect(screen.getByText("Probe Gate")).toBeTruthy();
  });

  it("renders spawn log section", async () => {
    render(ProviderDetail, { props: defaultProps });
    expect(await screen.findByText("Recent Spawns")).toBeTruthy();
    expect(screen.getByText("sess-abc")).toBeTruthy();
  });

  it("renders Delete button", async () => {
    render(ProviderDetail, { props: defaultProps });
    expect(await screen.findByText("Delete")).toBeTruthy();
  });
});

describe("ProviderDetail - secret handling", () => {
  it("does not send unchanged secret on full save", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Save All");
    fireEvent.click(screen.getByText("Save All"));
    await vi.waitFor(() => expect(api.apiSaveProviderDetail).toHaveBeenCalled());
    const payload = vi.mocked(api.apiSaveProviderDetail).mock.calls[0][3] as Record<string, string>;
    /* secret not touched → must be absent from payload */
    expect(Object.prototype.hasOwnProperty.call(payload, "api_key")).toBe(false);
    /* non-secret present */
    expect(payload["model"]).toBe("claude-3");
  });

  it("per-field save of untouched secret shows error, not api call", async () => {
    const { toastError } = await import("@wick-fe/common-stores");
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Save All");
    /* first Save button belongs to api_key (secret, not touched) */
    const saveBtns = screen.getAllByText("Save");
    fireEvent.click(saveBtns[0]);
    await vi.waitFor(() => expect(toastError).toHaveBeenCalled());
    expect(api.apiSaveConfigKey).not.toHaveBeenCalled();
  });

  it("password input renders with empty value (secret not echoed)", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("api_key");
    const pwdInput = document.querySelector("input[type=password]") as HTMLInputElement;
    expect(pwdInput.value).toBe("");
  });
});

describe("ProviderDetail - callbacks", () => {
  it("calls apiHookCheck when Check clicked", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Check");
    fireEvent.click(screen.getByText("Check"));
    await vi.waitFor(() => expect(api.apiHookCheck).toHaveBeenCalledWith("", "claude", "default", "pre_tool_use"));
  });

  it("calls apiHookEnable when Enable clicked", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Enable");
    fireEvent.click(screen.getByText("Enable"));
    await vi.waitFor(() => expect(api.apiHookEnable).toHaveBeenCalledWith("", "claude", "default", "pre_tool_use"));
  });

  it("calls apiProbeGate when Probe Gate clicked", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Probe Gate");
    fireEvent.click(screen.getByText("Probe Gate"));
    await vi.waitFor(() => expect(api.apiProbeGate).toHaveBeenCalledWith("claude", "default"));
  });

  it("calls onBack when back button clicked", async () => {
    const onBack = vi.fn();
    render(ProviderDetail, { props: { ...defaultProps, onBack } });
    await screen.findByText("← Providers");
    fireEvent.click(screen.getByText("← Providers"));
    expect(onBack).toHaveBeenCalled();
  });

  it("calls apiSaveConfigKey when per-field Save clicked", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("model");
    const saveBtns = screen.getAllByText("Save");
    /* model is the 2nd config field; its Save button is index 1 */
    fireEvent.click(saveBtns[1]);
    await vi.waitFor(() => expect(api.apiSaveConfigKey).toHaveBeenCalledWith("", "claude", "default", "model", "claude-3"));
  });
});
