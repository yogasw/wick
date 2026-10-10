import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ProviderDetail from "../ProviderDetail.svelte";
import { cardStacksMissingRhythm, expectCardRhythm } from "./cardRhythm.js";
import * as api from "$lib/api.js";
import * as mb from "$lib/managedbin.js";
import type { ProviderDetailResponse } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/managedbin.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("$lib/managedbin.js")>()),
  apiManagedList: vi.fn(),
}));
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: vi.fn(),
  toasts: { subscribe: vi.fn(() => vi.fn()) },
}));

// viewerCan: a non-admin who is not the owner — every permission off.
function viewerCan(): ProviderDetailResponse["Can"] {
  return Object.fromEntries(Object.keys(makeDetail().Can).map((k) => [k, false])) as ProviderDetailResponse["Can"];
}

function makeDetail(): ProviderDetailResponse {
  return {
    // Existing tests describe the ADMIN page; the read-only variant has
    // its own tests below.
    ReadOnly: false,
    CanManage: true,
    SecretsHidden: false,
    IsAdmin: true,
    Can: {
      Configure: true, Models: true, Env: true, ExtraArgs: true, Binary: true, ExtraMCPServers: true, ExternalSkills: true,
      Sandbox: true, AIRouterRawConfig: true, BorrowLogin: true, Rename: true, Delete: true, AIRouter: true, StorageSync: true,
      Rescan: true, ViewSessions: true,
    },
    OwnerPerms: { configure: true, binary: false, delete: true },
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
    DefaultModels: [],
    AIRouter: { Supported: true, Enabled: false, Provider: "9router", Routers: [{ ID: "9router", Name: "9router" }], Models: {}, KeySet: false, RawConfig: "", Preview: "" },
  };
}

const defaultProps = { base: "", type: "claude", name: "default", onBack: vi.fn(), onOpenSession: vi.fn() };

/* Collapsible sections remember their state per browser; start every
   test clean, with the CollapsibleSection cards open so the content tests
   below can reach their controls (default-closed is tested on its own). */
const OPEN_SECTIONS = ["binary", "models", "airouter", "hooks", "gate", "processes"];

beforeEach(() => {
  localStorage.clear();
  for (const k of OPEN_SECTIONS) localStorage.setItem(`wick.providers.section.detail.${k}`, "1");
  vi.mocked(api.apiGetProviderDetail).mockResolvedValue(makeDetail());
  // ProviderDetail embeds <RecentSpawns>, which fetches on mount.
  vi.mocked(api.apiGetSessions).mockResolvedValue({ Sessions: [], Page: 1, HasNext: false, Total: 0 });
  vi.mocked(api.apiSaveProviderDetail).mockResolvedValue(undefined);
  vi.mocked(api.apiSaveConfigKey).mockResolvedValue(undefined);
  vi.mocked(api.apiHookCheck).mockResolvedValue(undefined);
  vi.mocked(api.apiHookEnable).mockResolvedValue(undefined);
  vi.mocked(api.apiHookDisable).mockResolvedValue(undefined);
  vi.mocked(api.apiDeleteProvider).mockResolvedValue(undefined);
  vi.mocked(api.apiProbeGate).mockResolvedValue(undefined);
  vi.mocked(api.apiGetProviderCatalog).mockResolvedValue({ env: [], args: [] });
  vi.mocked(api.apiAIRouterStatus).mockResolvedValue({ installed: false, running: false, version: "", state: "stopped", prefPort: 0, boundPort: 0 });
  vi.mocked(api.apiAIRouterSlots).mockResolvedValue([]);
  vi.mocked(api.apiAIRouterModels).mockResolvedValue([]);
});

describe("ProviderDetail - rendering", () => {
  it("renders provider heading with type/name", async () => {
    render(ProviderDetail, { props: defaultProps });
    expect((await screen.findAllByText("claude/default")).length).toBeGreaterThan(0);
  });

  it("renders version badge when path found", async () => {
    render(ProviderDetail, { props: defaultProps });
    // Heading badge + the open Binary section's Version row.
    expect((await screen.findAllByText("1.2.3")).length).toBe(2);
  });

  it("renders resolved path in binary info", async () => {
    render(ProviderDetail, { props: defaultProps });
    // Body only: the header carries the version pill, not the long path.
    expect((await screen.findAllByText("/usr/bin/claude")).length).toBe(1);
    expect(screen.getByTestId("binary-version-pill").textContent).toBe("v1.2.3");
  });

  it("collapses Configuration / extra_args / env by default", async () => {
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Configuration");
    expect(screen.queryByText("max_concurrent")).toBeNull();
    expect(screen.queryByText("Save All")).toBeNull();
    const inputs = Array.from(document.querySelectorAll("input")) as HTMLInputElement[];
    expect(inputs.map((i) => i.value)).not.toContain("--foo");
    expect(inputs.map((i) => i.value)).not.toContain("FOO");
  });

  it("renders Configuration section with simple fields after expanding", async () => {
    render(ProviderDetail, { props: defaultProps });
    await fireEvent.click(await screen.findByText("Configuration"));
    /* "binary" also appears as a Command Gate row label */
    expect(screen.getAllByText("binary").length).toBeGreaterThan(0);
    expect(screen.getByText("max_concurrent")).toBeTruthy();
  });

  it("renders dropdown select for dropdown-type fields", async () => {
    render(ProviderDetail, { props: defaultProps });
    await fireEvent.click(await screen.findByText("Configuration"));
    await screen.findByText("send_mode");
    // Themed <Select> (common-ui), not a native <select>.
    const selects = screen.getAllByTestId("wick-select-trigger");
    expect(selects.length).toBeGreaterThan(0);
    expect(document.querySelectorAll("select").length).toBe(0);
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

  it("renders recent sessions section (via embedded RecentSpawns)", async () => {
    vi.mocked(api.apiGetSessions).mockResolvedValue({
      Sessions: [
        { SessionID: "sess-abc-1234", ProviderType: "claude", ProviderName: "default", SpawnCount: 2, LastStatus: "stopped", LastStarted: "2024-01-01T00:00:00Z", FirstMessage: "Hello", Origin: "web" },
      ],
      Page: 1, HasNext: false, Total: 1,
    });
    render(ProviderDetail, { props: defaultProps });
    await fireEvent.click(await screen.findByText("Recent Sessions"));
    expect(await screen.findByText("sess-abc")).toBeTruthy();
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
    await fireEvent.click(await screen.findByText("Configuration"));
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
    await fireEvent.click(await screen.findByText("extra_args"));
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
    await fireEvent.click(await screen.findByText("extra_args"));
    const addBtns = screen.getAllByText("+ Add Row");
    fireEvent.click(addBtns[0]);
    // Exclude the Recent Spawns search box (also an <input>) so "the fresh
    // row" is the last CONFIG input, not the search field.
    const inputs = (Array.from(document.querySelectorAll("input")) as HTMLInputElement[])
      .filter((i) => !(i.placeholder ?? "").toLowerCase().includes("search"));
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
    await fireEvent.click(await screen.findByText("env"));
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
    await fireEvent.click(await screen.findByText("env"));
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
    await fireEvent.click(await screen.findByText("env"));
    expect(screen.getByText(/No rows yet/)).toBeTruthy();
  });
});

describe("ProviderDetail - model selection (id|desc kvlist)", () => {
  function withModels(value: string, defaults: { id: string; desc: string }[] = []): ProviderDetailResponse {
    const data = makeDetail();
    data.ConfigFields = [
      { Key: "model_select", Value: "true", Type: "bool", Options: "", IsSecret: false, Description: "Show a model picker", Required: false },
      { Key: "models", Value: value, Type: "kvlist", Options: "id|desc", IsSecret: false, Description: "Models to offer", Required: false },
    ];
    data.DefaultModels = defaults;
    return data;
  }

  it("renders saved id/desc rows in their own columns", async () => {
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(
      withModels('[{"id":"opus","desc":"most capable"}]'),
    );
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Model selection");
    const vals = (Array.from(document.querySelectorAll("input")) as HTMLInputElement[]).map((i) => i.value);
    expect(vals).toContain("opus");
    expect(vals).toContain("most capable");
  });

  it("serializes rows as [{id,desc}] on save", async () => {
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(
      withModels('[{"id":"opus","desc":"most capable"}]'),
    );
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Model selection");
    const idInput = (Array.from(document.querySelectorAll("input")) as HTMLInputElement[]).find((i) => i.value === "opus")!;
    await fireEvent.blur(idInput);
    await vi.waitFor(() => expect(api.apiSaveConfigKey).toHaveBeenCalled());
    const lastCall = vi.mocked(api.apiSaveConfigKey).mock.calls.at(-1)!;
    expect(lastCall[3]).toBe("models");
    expect(JSON.parse(lastCall[4])).toEqual([{ id: "opus", desc: "most capable" }]);
  });

  it("Load defaults fills the rows with the catalog seed ids and descs", async () => {
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(
      withModels("", [
        { id: "opus", desc: "most capable" },
        { id: "sonnet", desc: "balanced" },
      ]),
    );
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Load defaults");
    await fireEvent.click(screen.getByText("Load defaults"));
    const vals = (Array.from(document.querySelectorAll("input")) as HTMLInputElement[]).map((i) => i.value);
    expect(vals).toContain("opus");
    expect(vals).toContain("balanced");
    const lastCall = vi.mocked(api.apiSaveConfigKey).mock.calls.at(-1)!;
    expect(lastCall[3]).toBe("models");
    expect(JSON.parse(lastCall[4])).toEqual([
      { id: "opus", desc: "most capable" },
      { id: "sonnet", desc: "balanced" },
    ]);
  });

  it("renders the models list exactly once (not again as a generic kvlist)", async () => {
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(
      withModels('[{"id":"opus","desc":"most capable"}]'),
    );
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Model selection");
    /* the `models` key label belongs to the generic section only */
    expect(screen.queryByText("models")).toBeNull();
    expect(screen.getAllByText("+ Add model")).toHaveLength(1);
    const idInputs = (Array.from(document.querySelectorAll("input")) as HTMLInputElement[])
      .filter((i) => i.value === "opus");
    expect(idInputs).toHaveLength(1);
  });

  it("hides the model list when selection is off", async () => {
    const data = withModels('[{"id":"opus","desc":"x"}]');
    data.ConfigFields[0].Value = "false";
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(data);
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Model selection");
    expect(screen.queryByText("+ Add model")).toBeNull();
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

describe("ProviderDetail - read-only viewer", () => {
  it("gives a manager a read-only summary, not the admin form", async () => {
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue({ ...makeDetail(), ReadOnly: true, CanManage: false, IsAdmin: false, Can: viewerCan() });
    render(ProviderDetail, { props: { base: "", type: "claude", name: "claude", onBack: vi.fn(), onOpenSession: vi.fn() } });

    const cfg = await screen.findByTestId("provider-config");
    expect(cfg.getAttribute("data-readonly")).toBe("1");
    // The summary, not the editor.
    expect(screen.getByText("CONFIGURATION")).toBeTruthy();
    // And crucially NOT the AI Router card: mounting it fires admin-only
    // requests that answer 403 in a manager's console.
    expect(screen.queryByText("AI Router")).toBeNull();
    // Enable/disable, delete and rename are admin-only: gone, with the
    // on/off state left as a plain badge.
    expect(screen.queryByText(/click to disable/i)).toBeNull();
    expect(screen.queryByText("Delete")).toBeNull();
    expect(screen.queryByTitle("Rename this provider")).toBeNull();
    expect(screen.getByText("Enabled")).toBeTruthy();
  });

  it("shows the configuration normally for an admin", async () => {
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(makeDetail());
    render(ProviderDetail, { props: { base: "", type: "claude", name: "claude", onBack: vi.fn(), onOpenSession: vi.fn() } });
    await screen.findByText(/resolved/i);
    expect(screen.getByTestId("provider-config").getAttribute("data-readonly")).toBeNull();
    // The admin still gets the full editor.
    expect(screen.queryByText("CONFIGURATION")).toBeNull();
  });
});

describe("ProviderDetail - card rhythm", () => {
  /* The bug this pins: the editable sections are wrapped in one div so
     the branch has a single root, and that wrapper took every card out
     of the page's `space-y-4`. The cards then rendered flush — headers
     touching the card above, the "Advanced" rule glued to extra_args —
     which is only visible to a person looking at the page, never to a
     test that asserts on text. */
  it("spaces the cards it stacks, so none of them render flush", async () => {
    const { container } = render(ProviderDetail, { props: defaultProps });
    await screen.findByTestId("provider-config");
    expectCardRhythm(container);
  });

  it("spaces the read-only summary too", async () => {
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue({ ...makeDetail(), ReadOnly: true, CanManage: false, IsAdmin: false, Can: viewerCan() });
    const { container } = render(ProviderDetail, { props: defaultProps });
    await screen.findByTestId("provider-config");
    expectCardRhythm(container);
  });

  /* The check has to actually catch it — a guard that passes on the
     broken markup is worse than none, because it reads as covered. */
  it("catches a stack that forgot its spacing", () => {
    const el = document.createElement("div");
    el.innerHTML =
      '<div class="rounded-xl border p-5"></div><div class="rounded-xl border p-5"></div>';
    expect(cardStacksMissingRhythm(el)).toHaveLength(1);
    el.setAttribute("class", "space-y-4");
    expect(cardStacksMissingRhythm(el)).toHaveLength(0);
  });
});

describe("ProviderDetail - opencode MCP + hosted model settings", () => {
  it("renders extra MCP as a textarea and warns about hosted models / a missing model", async () => {
    const d = makeDetail();
    d.Instance = { ...d.Instance, Type: "opencode", Name: "oc" };
    d.ConfigFields = [
      { Key: "extra_mcp_servers", Value: "{}", Type: "textarea", Options: "", IsSecret: false, Description: "Extra MCP", Required: false },
      { Key: "opencode_model", Value: "", Type: "text", Options: "", IsSecret: false, Description: "model", Required: false },
      { Key: "opencode_allow_hosted", Value: "true", Type: "dropdown", Options: "false|true", IsSecret: false, Description: "hosted", Required: false },
    ];
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(d);
    render(ProviderDetail, { props: { ...defaultProps, type: "opencode", name: "oc" } });
    await fireEvent.click(await screen.findByText("Configuration"));
    expect((await screen.findByLabelText("extra_mcp_servers")).tagName).toBe("TEXTAREA");
    expect(screen.getByTestId("opencode-hosted-warning").textContent).toContain("opencode's servers");
    expect(screen.getByTestId("opencode-model-missing")).toBeTruthy();
  });
});

describe("ProviderDetail - omp/opencode live model list", () => {
  function liveDetail(live: boolean) {
    const d = makeDetail();
    d.Instance = { ...d.Instance, Type: "opencode", Name: "oc" };
    d.ConfigFields = [
      { Key: "live_models", Value: live ? "true" : "false", Type: "bool", Options: "", IsSecret: false, Description: "live", Required: false },
      { Key: "live_model_filter", Value: "gpt", Type: "text", Options: "", IsSecret: false, Description: "filter", Required: false },
      { Key: "live_model_default", Value: "", Type: "text", Options: "", IsSecret: false, Description: "default", Required: false },
      { Key: "model_select", Value: "true", Type: "bool", Options: "", IsSecret: false, Description: "", Required: false },
      { Key: "models", Value: "[]", Type: "kvlist", Options: "id|desc", IsSecret: false, Description: "", Required: false },
    ];
    return d;
  }

  it("live mode shows the CLI list panel instead of the manual list; toggle saves live_models", async () => {
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(liveDetail(true));
    vi.mocked(api.apiGetCLIModels).mockResolvedValue({ models: [{ id: "openai/gpt-5.5" }, { id: "google/gemini-3" }], hostedAllowed: false, fetchedAt: "" });
    render(ProviderDetail, { props: { ...defaultProps, type: "opencode", name: "oc" } });
    expect(await screen.findByTestId("live-models-panel")).toBeTruthy();
    expect(screen.queryByText("+ Add model")).toBeNull();
    // live keys never leak into the generic Configuration rows
    expect(screen.queryByLabelText("live_model_filter")).toBeNull();
    await fireEvent.click(screen.getByTestId("model-source-manual"));
    expect(api.apiSaveConfigKey).toHaveBeenCalledWith("", "opencode", "oc", "live_models", "false");
  });

  it("manual mode keeps the curated list", async () => {
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(liveDetail(false));
    render(ProviderDetail, { props: { ...defaultProps, type: "opencode", name: "oc" } });
    expect(await screen.findByTestId("model-source-toggle")).toBeTruthy();
    expect(screen.queryByTestId("live-models-panel")).toBeNull();
    expect(screen.getByText("+ Add model")).toBeTruthy();
  });
});

describe("ProviderDetail - server mode + Load Claude/Codex skills toggles", () => {
  it("renders both as switches; server mode off shows the run-per-turn note", async () => {
    const d = makeDetail();
    d.Instance = { ...d.Instance, Type: "opencode", Name: "oc" };
    d.ConfigFields = [
      { Key: "server_mode", Value: "true", Type: "bool", Options: "", IsSecret: false, Description: "server", Required: false },
      { Key: "load_external_skills", Value: "false", Type: "bool", Options: "", IsSecret: false, Description: "skills", Required: false },
    ];
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(d);
    render(ProviderDetail, { props: { ...defaultProps, type: "opencode", name: "oc" } });
    // Closed by default, but the header already says what is inside.
    expect((await screen.findByTestId("section-config")).getAttribute("data-open")).toBe("0");
    expect(screen.getByText(/Server mode: on · 2 fields/)).toBeTruthy();
    await fireEvent.click(await screen.findByText("Configuration"));
    const server = await screen.findByTestId("server-mode-toggle");
    expect(server.getAttribute("aria-checked")).toBe("true");
    expect(screen.getByTestId("load-skills-toggle").getAttribute("aria-checked")).toBe("false");
    expect(screen.getByTestId("field-label-load_external_skills").textContent).toBe("Load Claude/Codex skills");
    expect(screen.queryByTestId("run-per-turn-note")).toBeNull();
    await fireEvent.click(server);
    expect(server.getAttribute("aria-checked")).toBe("false");
    expect(screen.getByTestId("run-per-turn-note")).toBeTruthy();
  });

  it("renders auto_retry_model as a labelled switch, off by default", async () => {
    const d = makeDetail();
    d.Instance = { ...d.Instance, Type: "omp", Name: "o" };
    d.ConfigFields = [
      { Key: "auto_retry_model", Value: "false", Type: "bool", Options: "", IsSecret: false, Description: "retry", Required: false },
    ];
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(d);
    render(ProviderDetail, { props: { ...defaultProps, type: "omp", name: "o" } });
    await fireEvent.click(await screen.findByText("Configuration"));
    const sw = await screen.findByTestId("auto-retry-model-toggle");
    expect(sw.getAttribute("aria-checked")).toBe("false");
    expect(screen.getByTestId("field-label-auto_retry_model").textContent).toBe("Auto-retry with the next model on access error");
  });
});

describe("ProviderDetail - layout", () => {
  it("puts Connection first and keeps the other sections collapsed by default", async () => {
    localStorage.clear();
    const { container } = render(ProviderDetail, { props: defaultProps });
    const first = await screen.findByTestId("detail-connection-first");
    const binaryHeader = await screen.findByText("Binary");
    // Connection precedes the Binary card in document order.
    expect(first.compareDocumentPosition(binaryHeader) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.queryByText("Probe Gate")).toBeNull();
    const open = [...container.querySelectorAll("[data-open]")].map((el) => el.getAttribute("data-open"));
    expect(open.length).toBeGreaterThan(0);
    expect(open.every((v) => v === "0")).toBe(true);
  });

  it("remembers an opened section in localStorage", async () => {
    localStorage.clear();
    render(ProviderDetail, { props: defaultProps });
    await fireEvent.click(await screen.findByText("Command Gate"));
    expect(localStorage.getItem("wick.providers.section.detail.gate")).toBe("1");
    expect(await screen.findByText("Probe Gate")).toBeTruthy();
  });
});

describe("ProviderDetail - Binary section", () => {
  it("non-managed type: one Binary section, path + version rows, no managed rows", async () => {
    render(ProviderDetail, { props: defaultProps });
    const sec = await screen.findByTestId("section-binary");
    expect(sec.textContent).toContain("Resolved path");
    expect(sec.textContent).toContain("/usr/bin/claude");
    expect(sec.textContent).not.toContain("managed by wick");
    expect(screen.queryByTestId("managed-binary-panel")).toBeNull();
  });

  it("managed type: ONE Binary section holds the managed info; header = version + managed pill", async () => {
    localStorage.removeItem("wick.providers.section.detail.binary");
    const d = makeDetail();
    d.Instance = { ...d.Instance, Type: "opencode", Name: "oc" };
    d.Path = "/home/x/.support-tools/providers/bin/opencode/versions/1.18.33/opencode";
    d.Version = "1.18.33";
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue(d);
    vi.mocked(mb.apiManagedList).mockResolvedValue({
      isAdmin: true,
      types: [mb.normalizeManaged({ type: "opencode", enabled: true, host_label: "linux-x64 · glibc · AVX2", current: "1.18.33", current_path: d.Path, latest: { tag: "v1.18.33", version: "1.18.33" } })],
    });
    render(ProviderDetail, { props: { ...defaultProps, type: "opencode", name: "oc" } });
    const sec = await screen.findByTestId("section-binary");
    // collapsed by default: header summary only, no long path
    expect(sec.getAttribute("data-open")).toBe("0");
    expect(sec.textContent).toContain("v1.18.33");
    expect(sec.textContent).toContain("managed by wick");
    expect(sec.textContent).not.toContain("/opencode/versions/");
    expect(screen.queryByText("Binary · opencode")).toBeNull();
    expect(screen.getAllByText("Binary")).toHaveLength(1);

    await fireEvent.click(screen.getByText("Binary"));
    const panel = await screen.findByTestId("managed-binary-panel");
    expect(sec.contains(panel)).toBe(true);
    expect((await screen.findByTestId("managed-current")).textContent).toBe("v1.18.33");
    expect(screen.getByTestId("managed-host").textContent).toBe("linux-x64 · glibc · AVX2");
    expect(sec.textContent).toContain(d.Path);
    expect(localStorage.getItem("wick.providers.section.detail.binary")).toBe("1");
  });
});

describe("ProviderDetail - Activity", () => {
  it("Token Usage is collapsed by default and mounts the report only when opened", async () => {
    render(ProviderDetail, { props: defaultProps });
    const sec = await screen.findByTestId("section-activity");
    expect(sec.getAttribute("data-open")).toBe("0");
    expect(sec.textContent).toContain("Token Usage");
    expect(sec.textContent).toContain("claude/default");
    expect(screen.queryByText("Refresh")).toBeNull();
    await fireEvent.click(screen.getByText("Token Usage"));
    expect(sec.getAttribute("data-open")).toBe("1");
    expect(await screen.findByText("Refresh")).toBeTruthy();
    expect(localStorage.getItem("wick.providers.section.detail.activity")).toBe("1");
  });
});

describe("ProviderDetail - owner permissions", () => {
  it("shows the card to an admin, seeded from OwnerPerms, and saves the ticks", async () => {
    vi.mocked(api.apiSaveOwnerPerms).mockImplementation(async (_b, _t, _n, perms) => perms);
    render(ProviderDetail, { props: defaultProps });
    await fireEvent.click(await screen.findByText("Owner permissions"));
    const binary = (await screen.findByTestId("owner-perm-binary")) as HTMLInputElement;
    const del = screen.getByTestId("owner-perm-delete") as HTMLInputElement;
    expect(binary.checked).toBe(false);
    expect(del.checked).toBe(true);
    await fireEvent.click(binary);
    await fireEvent.click(screen.getByTestId("owner-perms-save"));
    expect(api.apiSaveOwnerPerms).toHaveBeenCalledWith(
      defaultProps.base, "claude", "default",
      expect.objectContaining({ binary: true, delete: true }),
    );
  });

  it("hides the card from a non-admin", async () => {
    vi.mocked(api.apiGetProviderDetail).mockResolvedValue({ ...makeDetail(), IsAdmin: false, OwnerPerms: {} });
    render(ProviderDetail, { props: defaultProps });
    await screen.findByText("Configuration");
    expect(screen.queryByText("Owner permissions")).toBeNull();
  });
});
