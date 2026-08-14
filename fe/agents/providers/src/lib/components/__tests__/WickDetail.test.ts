import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import WickDetail from "../WickDetail.svelte";
import * as api from "$lib/api.js";
import type { WickConfig } from "$lib/api.js";

vi.mock("$lib/api.js");
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: vi.fn(),
  toasts: { subscribe: vi.fn(() => vi.fn()) },
}));

function makeConfig(): WickConfig {
  return {
    models: [
      {
        ID: "m_1",
        Kind: "google",
        Label: "Gemini Flash",
        Model: "gemini-flash-latest",
        KeyMasked: "••••••••3kQ",
        HasKey: true,
        BaseURL: "",
        APIFormat: "gemini",
        MaxOutputTokens: 0,
        Default: true,
        Disabled: false,
        Temperature: null,
        TopP: null,
        ThinkingBudget: null,
        RawConfig: "",
        Headers: "",
        LiveSet: false,
        DiscoveryFilter: "",
        DefaultVendorModel: "",
      },
      {
        ID: "m_2",
        Kind: "openrouter",
        Label: "Grok 4.5",
        Model: "x-ai/grok-4.5",
        KeyMasked: "••••••••aB2",
        HasKey: true,
        BaseURL: "https://openrouter.ai/api/v1",
        APIFormat: "openai_chat",
        MaxOutputTokens: 0,
        Default: false,
        Disabled: false,
        Temperature: null,
        TopP: null,
        ThinkingBudget: null,
        RawConfig: "",
        Headers: "User-Agent: RooCode/3.53.0",
        LiveSet: false,
        DiscoveryFilter: "",
        DefaultVendorModel: "",
      },
    ],
    settings: {
      ShellToolDisabled: false,
      ShowCapabilities: true,
      EnableStreaming: true,
      CapabilityMode: "icon",
      Connectors: [],
      MaxContextTokens: 0,
      MaxTurns: 0,
      MaxConsecErrors: 0,
      MaxTurnMinutes: 0,
      MaxModelRetries: 0,
      ModelCallTimeoutSec: 0,
      Temperature: null,
      TopP: null,
      ThinkingBudget: null,
      RawConfig: "",
    },
  };
}

beforeEach(() => {
  vi.mocked(api.apiGetWickConfig).mockResolvedValue(makeConfig());
  vi.mocked(api.apiSaveWickModel).mockResolvedValue({ status: "ok", id: "m_new" });
  vi.mocked(api.apiDeleteWickModel).mockResolvedValue(undefined);
  vi.mocked(api.apiSetWickModelDefault).mockResolvedValue(undefined);
  vi.mocked(api.apiSaveWickSettings).mockResolvedValue(undefined);
  vi.mocked(api.apiDiscoverWickModels).mockResolvedValue({ models: [], error: "" });
  // RecentSpawns (embedded) loads sessions on mount.
  vi.mocked(api.apiGetSessions).mockResolvedValue({ Sessions: [], Page: 1, HasNext: false, Total: 0 });
});

const props = () => ({ base: "/wick", onBack: vi.fn(), onOpenSession: vi.fn() });

describe("WickDetail", () => {
  it("renders the built-in header + single-instance chip", async () => {
    render(WickDetail, { props: props() });
    expect(await screen.findByText("Built-in")).toBeTruthy();
    expect(screen.getByText(/Single instance/)).toBeTruthy();
  });

  it("renders the models table from config", async () => {
    render(WickDetail, { props: props() });
    expect(await screen.findByText("gemini-flash-latest")).toBeTruthy();
    expect(screen.getByText("x-ai/grok-4.5")).toBeTruthy();
    expect(screen.getByText("Google")).toBeTruthy();
    expect(screen.getByText("OpenRouter")).toBeTruthy();
  });

  it("shows the Provider settings card", async () => {
    render(WickDetail, { props: props() });
    expect(await screen.findByText("Provider settings")).toBeTruthy();
    expect(screen.getByText("Save settings")).toBeTruthy();
  });

  it("opens the Add custom model modal", async () => {
    render(WickDetail, { props: props() });
    await screen.findByText("gemini-flash-latest");
    await fireEvent.click(screen.getByText("Add model"));
    expect(await screen.findByText("Add custom model")).toBeTruthy();
    expect(screen.getByPlaceholderText(/Filter models/)).toBeTruthy();
  });

  it("batch-adds several models in Multiple mode", async () => {
    vi.mocked(api.apiDiscoverWickModels).mockResolvedValue({
      models: [
        { id: "gpt-5.2", label: "GPT 5.2" },
        { id: "o4-mini", label: "o4 mini" },
      ],
      error: "",
    });
    render(WickDetail, { props: props() });
    await screen.findByText("gemini-flash-latest");
    await fireEvent.click(screen.getByText("Add model"));
    await screen.findByText("Add custom model");

    // Typing a key triggers discovery; wait for the two mocked models.
    await fireEvent.input(screen.getByLabelText("API key"), { target: { value: "sk-test" } });
    await screen.findByText("gpt-5.2");

    // Multiple + Manual mode, then tick both rows explicitly.
    await fireEvent.click(screen.getByText("Multiple"));
    await fireEvent.click(screen.getByText("Manual"));
    await fireEvent.click(screen.getByText("gpt-5.2"));
    await fireEvent.click(screen.getByText("o4-mini"));

    // Footer button reflects the count and saves one entry per model.
    vi.mocked(api.apiSaveWickModel).mockClear();
    await fireEvent.click(screen.getByText(/Add 2 models/));
    expect(api.apiSaveWickModel).toHaveBeenCalledTimes(2);
    expect(api.apiSaveWickModel).toHaveBeenCalledWith("/wick", expect.objectContaining({ model: "gpt-5.2" }));
    expect(api.apiSaveWickModel).toHaveBeenCalledWith("/wick", expect.objectContaining({ model: "o4-mini" }));
  });

  it("live mode saves ONE entry storing the filter (not per-model)", async () => {
    vi.mocked(api.apiDiscoverWickModels).mockResolvedValue({
      models: [
        { id: "gpt-5.2", label: "GPT 5.2" },
        { id: "gpt-5.2-mini", label: "GPT 5.2 mini" },
        { id: "claude-x", label: "Claude X" },
      ],
      error: "",
    });
    render(WickDetail, { props: props() });
    await screen.findByText("gemini-flash-latest");
    await fireEvent.click(screen.getByText("Add model"));
    await screen.findByText("Add custom model");
    await fireEvent.input(screen.getByLabelText("API key"), { target: { value: "sk-test" } });
    await screen.findByText("gpt-5.2");

    // Multiple defaults to Live. Filter "gpt -mini" — the preview shows 1 match
    // (gpt-5.2), but saving stores ONE live entry carrying the filter.
    await fireEvent.click(screen.getByText("Multiple"));
    await fireEvent.input(screen.getByPlaceholderText(/Filter models/), { target: { value: "gpt -mini" } });
    vi.mocked(api.apiSaveWickModel).mockClear();
    await fireEvent.click(screen.getByText("Add live set"));
    expect(api.apiSaveWickModel).toHaveBeenCalledTimes(1);
    expect(api.apiSaveWickModel).toHaveBeenCalledWith(
      "/wick",
      expect.objectContaining({ discovery_filter: "gpt -mini", model: "" }),
    );
  });

  it("filters discovered models with an exclude term", async () => {
    vi.mocked(api.apiDiscoverWickModels).mockResolvedValue({
      models: [
        { id: "gpt-5.2", label: "GPT 5.2" },
        { id: "gpt-5.2-mini", label: "GPT 5.2 mini" },
      ],
      error: "",
    });
    render(WickDetail, { props: props() });
    await screen.findByText("gemini-flash-latest");
    await fireEvent.click(screen.getByText("Add model"));
    await screen.findByText("Add custom model");
    await fireEvent.input(screen.getByLabelText("API key"), { target: { value: "sk-test" } });
    await screen.findByText("gpt-5.2");

    // "gpt -mini" keeps the base id, excludes the mini variant.
    await fireEvent.input(screen.getByPlaceholderText(/Filter models/), { target: { value: "gpt -mini" } });
    expect(screen.getByText("gpt-5.2")).toBeTruthy();
    expect(screen.queryByText("gpt-5.2-mini")).toBeNull();
  });

  it("sets a non-default model as default", async () => {
    render(WickDetail, { props: props() });
    await screen.findByText("x-ai/grok-4.5");
    // Only the non-default row exposes a "Set as default" control.
    await fireEvent.click(screen.getByLabelText("Set as default"));
    expect(api.apiSetWickModelDefault).toHaveBeenCalledWith("/wick", "m_2");
  });

  it("deletes a model through the confirm dialog", async () => {
    render(WickDetail, { props: props() });
    await screen.findByText("gemini-flash-latest");
    const kebabBtns = screen.getAllByLabelText(/Actions for/);
    await fireEvent.click(kebabBtns[0]);
    // The kebab popup's "Delete" row opens the confirm dialog; its own
    // destructive button shares the same visible text, so scope to the
    // dialog via role — the LAST "Delete" match once the popup is open.
    const deleteRow = (await screen.findAllByText("Delete")).at(-1)!;
    await fireEvent.click(deleteRow);
    await fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(api.apiDeleteWickModel).toHaveBeenCalledWith("/wick", "m_1");
  });

  it("saves provider settings", async () => {
    render(WickDetail, { props: props() });
    await screen.findByText("Provider settings");
    await fireEvent.click(screen.getByText("Save settings"));
    expect(api.apiSaveWickSettings).toHaveBeenCalled();
    const arg = vi.mocked(api.apiSaveWickSettings).mock.calls[0][1];
    expect(arg.shell_tool_disabled).toBe(false);
  });

  it("editing an existing model prefills the display name", async () => {
    render(WickDetail, { props: props() });
    await screen.findByText("gemini-flash-latest");
    const kebabBtns = screen.getAllByLabelText(/Actions for/);
    await fireEvent.click(kebabBtns[0]);
    await fireEvent.click(await screen.findByText("Edit"));
    expect(await screen.findByText("Edit custom model")).toBeTruthy();
    const nameInput = screen.getByLabelText("Display name (optional)") as HTMLInputElement;
    expect(nameInput.value).toBe("Gemini Flash");
  });

  // Advanced options, Raw model config and Custom headers are ALL collapsed
  // on open — even when editing a model that has values stored in them. The
  // user expands only what they came to change.
  it("keeps Advanced options and both escape hatches collapsed on open", async () => {
    render(WickDetail, { props: props() });
    await screen.findByText("x-ai/grok-4.5");
    // m_2 carries a stored header blob — it must still open collapsed.
    const kebabBtns = screen.getAllByLabelText(/Actions for/);
    await fireEvent.click(kebabBtns[1]);
    await fireEvent.click(await screen.findByText("Edit"));
    await screen.findByText("Edit custom model");
    expect(screen.queryByLabelText(/Custom headers/)).toBeNull();
    expect(screen.queryByLabelText(/Raw model config/)).toBeNull();

    // Expanding Advanced reveals the two sub-toggles, still collapsed.
    await fireEvent.click(screen.getByText("Advanced options"));
    expect(screen.queryByLabelText(/Custom headers/)).toBeNull();
    expect(screen.queryByLabelText(/Raw model config/)).toBeNull();
    expect(screen.getByText(/Custom headers/)).toBeTruthy();
    expect(screen.getByText(/Raw model config/)).toBeTruthy();
  });

  it("prefills stored custom headers once the section is expanded", async () => {
    render(WickDetail, { props: props() });
    await screen.findByText("x-ai/grok-4.5");
    // m_2 is the second row and carries a stored header blob.
    const kebabBtns = screen.getAllByLabelText(/Actions for/);
    await fireEvent.click(kebabBtns[1]);
    await fireEvent.click(await screen.findByText("Edit"));
    await screen.findByText("Edit custom model");
    await fireEvent.click(screen.getByText("Advanced options"));
    await fireEvent.click(screen.getByText(/Custom headers/));
    const headersInput = screen.getByLabelText(/Custom headers/) as HTMLTextAreaElement;
    expect(headersInput.value).toBe("User-Agent: RooCode/3.53.0");
  });

  // Typing headers must re-run discovery the same way typing an API key or
  // base URL does — a gateway that needs the header to serve /models can
  // only list once it's set, and that has to work before saving the model.
  it("re-runs model discovery when custom headers change", async () => {
    vi.useFakeTimers();
    try {
      render(WickDetail, { props: props() });
      await vi.advanceTimersByTimeAsync(0);
      await fireEvent.click(screen.getByText("Add model"));
      await fireEvent.input(screen.getByLabelText("API key"), { target: { value: "sk-test" } });
      await vi.advanceTimersByTimeAsync(400);

      await fireEvent.click(screen.getByText("Advanced options"));
      await fireEvent.click(screen.getByText(/Custom headers/));
      vi.mocked(api.apiDiscoverWickModels).mockClear();
      await fireEvent.input(screen.getByLabelText(/Custom headers/), {
        target: { value: "X-Org-Id: abc123" },
      });
      await vi.advanceTimersByTimeAsync(400);

      expect(api.apiDiscoverWickModels).toHaveBeenCalledWith(
        "/wick",
        expect.objectContaining({ headers: "X-Org-Id: abc123", api_key: "sk-test" }),
      );
    } finally {
      vi.useRealTimers();
    }
  });

  // Re-discovery must not blank the list: swapping the rows for a
  // "Discovering models…" placeholder on every keystroke makes the picker
  // flicker. Keep the previous results on screen until the new ones land.
  it("keeps the model list visible while re-discovering", async () => {
    vi.useFakeTimers();
    try {
      vi.mocked(api.apiDiscoverWickModels).mockResolvedValue({
        models: [{ id: "gpt-5.2", label: "GPT 5.2" }],
        error: "",
      });
      render(WickDetail, { props: props() });
      await vi.advanceTimersByTimeAsync(0);
      await fireEvent.click(screen.getByText("Add model"));
      await fireEvent.input(screen.getByLabelText("API key"), { target: { value: "sk-test" } });
      await vi.advanceTimersByTimeAsync(400);
      expect(screen.getByText("gpt-5.2")).toBeTruthy();

      // Never resolves — the refetch stays in flight so we can observe the
      // list mid-discovery.
      vi.mocked(api.apiDiscoverWickModels).mockReturnValue(new Promise(() => {}));
      await fireEvent.click(screen.getByText("Advanced options"));
      await fireEvent.click(screen.getByText(/Custom headers/));
      await fireEvent.input(screen.getByLabelText(/Custom headers/), {
        target: { value: "X-Org-Id: abc123" },
      });
      await vi.advanceTimersByTimeAsync(400);

      // Rows survive the in-flight refetch; only a subtle marker appears.
      expect(screen.getByText("gpt-5.2")).toBeTruthy();
      expect(screen.queryByText("Discovering models…")).toBeNull();
      expect(screen.getByText("Refreshing…")).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("saves custom headers, normalizing a pasted curl block", async () => {
    render(WickDetail, { props: props() });
    await screen.findByText("gemini-flash-latest");
    await fireEvent.click(screen.getByText("Add model"));
    await screen.findByText("Add custom model");
    await fireEvent.input(screen.getByLabelText("Model ID"), { target: { value: "gpt-5.2" } });
    // Headers live under Advanced options → Custom headers, both collapsed.
    await fireEvent.click(screen.getByText("Advanced options"));
    await fireEvent.click(screen.getByText(/Custom headers/));
    await fireEvent.input(screen.getByLabelText(/Custom headers/), {
      target: {
        value: "--header 'X-Stainless-OS: Linux' \\\n--header 'User-Agent: RooCode/3.53.0' \\",
      },
    });
    vi.mocked(api.apiSaveWickModel).mockClear();
    // "Add model" is both the page button and the modal's submit — the
    // modal footer is the last match once the modal is open.
    await fireEvent.click(screen.getAllByText("Add model").at(-1)!);
    expect(api.apiSaveWickModel).toHaveBeenCalledWith(
      "/wick",
      expect.objectContaining({ headers: "X-Stainless-OS: Linux\nUser-Agent: RooCode/3.53.0" }),
    );
  });
});
