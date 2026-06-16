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
      { Key: "binary", Value: "claude", Type: "text", Options: "", IsSecret: false, Description: "Binary path override", Required: false },
      { Key: "max_concurrent", Value: "4", Type: "number", Options: "", IsSecret: false, Description: "Max parallel spawns", Required: false },
      { Key: "send_mode", Value: "default", Type: "dropdown", Options: "default|append|queue|spawn", IsSecret: false, Description: "Send mode", Required: false },
      { Key: "extra_args", Value: "[{\"value\":\"--foo\"},{\"value\":\"--bar\"}]", Type: "kvlist", Options: "value", IsSecret: false, Description: "Extra CLI args", Required: false },
      { Key: "env", Value: "[{\"key\":\"FOO\",\"value\":\"1\"}]", Type: "kvlist", Options: "key|value", IsSecret: false, Description: "Environment variables", Required: false },
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
    expect(await screen.findByRole("heading", { name: "claude/default" })).toBeTruthy();
  });

  it("renders version badge when path found", async () => {
    render(ProviderDetail, { props: defaultProps });
    expect(await screen.findByText("1.2.3")).toBeTruthy();
  });

  it("renders resolved path in binary info", async () => {
    render(ProviderDetail, { props: defaultProps });
    expect(await screen.findByText("/usr/bin/claude")).toBeTruthy();
  });

  it("renders Configuration section with simple fields", async () => {
    render(ProviderDetail, { props: defaultProps });
    expect(await screen.findByText("Configuration")).toBeTruthy();
    /* "binary" also appears as a Command Gate row label */
    expect(screen.getAllByText("binary").length).toBeGreaterThan(0);
    expect(screen.getByText("max_concurrent")).toBeTruthy();
  });

  it("renders dropdown select for dropdown-type fields", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("send_mode");
    const selects = document.querySelectorAll("select");
    expect(selects.length).toBeGreaterThan(0);
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

describe("ProviderDetail - enable/disable toggle", () => {
  it("renders Enabled toggle when not disabled", async () => {
    render(ProviderDetail, { props: defaultProps });
    expect(await screen.findByText("Enabled — click to disable")).toBeTruthy();
  });

  it("renders Disabled toggle when disabled", async () => {
    const data = makeDetail();
    data.Instance.Disabled = true;
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(data);
    render(ProviderDetail, { props: defaultProps });
    expect(await screen.findByText("Disabled — click to enable")).toBeTruthy();
  });

  it("calls apiSaveConfigKey with disabled=true when enabled toggle clicked", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Enabled — click to disable");
    fireEvent.click(screen.getByText("Enabled — click to disable"));
    await vi.waitFor(() => expect(api.apiSaveConfigKey).toHaveBeenCalledWith("", "claude", "default", "disabled", "true"));
  });

  it("calls apiSaveConfigKey with disabled=false when disabled toggle clicked", async () => {
    const data = makeDetail();
    data.Instance.Disabled = true;
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(data);
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Disabled — click to enable");
    fireEvent.click(screen.getByText("Disabled — click to enable"));
    await vi.waitFor(() => expect(api.apiSaveConfigKey).toHaveBeenCalledWith("", "claude", "default", "disabled", "false"));
  });
});

describe("ProviderDetail - simple field save", () => {
  it("Save All sends simple fields only", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Save All");
    fireEvent.click(screen.getByText("Save All"));
    await vi.waitFor(() => expect(api.apiSaveProviderDetail).toHaveBeenCalled());
    const payload = vi.mocked(api.apiSaveProviderDetail).mock.calls[0][3] as Record<string, string>;
    expect(payload["binary"]).toBe("claude");
    expect(payload["max_concurrent"]).toBe("4");
    expect(payload["send_mode"]).toBe("default");
    /* kvlist fields are never in the simple payload */
    expect(Object.prototype.hasOwnProperty.call(payload, "extra_args")).toBe(false);
    expect(Object.prototype.hasOwnProperty.call(payload, "env")).toBe(false);
  });
});

describe("ProviderDetail - value-list editor (extra_args)", () => {
  it("renders existing value rows", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("extra_args");
    const inputs = Array.from(document.querySelectorAll("input")) as HTMLInputElement[];
    const vals = inputs.map((i) => i.value);
    expect(vals).toContain("--foo");
    expect(vals).toContain("--bar");
  });

  it("serializes value rows as [{value}] on save", async () => {
    const data = makeDetail();
    data.ConfigFields = data.ConfigFields.filter((f) => f.Key === "extra_args");
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(data);
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("extra_args");
    const addBtns = screen.getAllByText("+ Add Row");
    fireEvent.click(addBtns[0]);
    const inputs = Array.from(document.querySelectorAll("input")) as HTMLInputElement[];
    const fresh = inputs[inputs.length - 1];
    await fireEvent.input(fresh, { target: { value: "--baz" } });
    await fireEvent.blur(fresh);
    await vi.waitFor(() => expect(api.apiSaveConfigKey).toHaveBeenCalled());
    const lastCall = vi.mocked(api.apiSaveConfigKey).mock.calls.at(-1)!;
    expect(lastCall[3]).toBe("extra_args");
    const parsed = JSON.parse(lastCall[4]) as Array<Record<string, string>>;
    expect(parsed).toEqual([{ value: "--foo" }, { value: "--bar" }, { value: "--baz" }]);
  });
});

describe("ProviderDetail - key-value editor (env)", () => {
  it("renders existing key-value rows", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("env");
    const inputs = Array.from(document.querySelectorAll("input")) as HTMLInputElement[];
    const vals = inputs.map((i) => i.value);
    expect(vals).toContain("FOO");
    expect(vals).toContain("1");
  });

  it("serializes rows as [{key,value}] on save", async () => {
    const data = makeDetail();
    data.ConfigFields = data.ConfigFields.filter((f) => f.Key === "env");
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(data);
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("env");
    const inputs = Array.from(document.querySelectorAll("input")) as HTMLInputElement[];
    await fireEvent.blur(inputs[0]);
    await vi.waitFor(() => expect(api.apiSaveConfigKey).toHaveBeenCalled());
    const lastCall = vi.mocked(api.apiSaveConfigKey).mock.calls.at(-1)!;
    expect(lastCall[3]).toBe("env");
    const parsed = JSON.parse(lastCall[4]) as Array<Record<string, string>>;
    expect(parsed).toEqual([{ key: "FOO", value: "1" }]);
  });

  it("shows empty state when no rows", async () => {
    const data = makeDetail();
    data.ConfigFields = [{ Key: "env", Value: "", Type: "kvlist", Options: "key|value", IsSecret: false, Description: "", Required: false }];
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(data);
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("env");
    expect(screen.getByText(/No rows yet/)).toBeTruthy();
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
    await screen.findByRole("button", { name: "Providers" });
    fireEvent.click(screen.getByRole("button", { name: "Providers" }));
    expect(onBack).toHaveBeenCalled();
  });
});
