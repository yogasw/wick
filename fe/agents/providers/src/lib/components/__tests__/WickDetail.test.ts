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
      },
    ],
    settings: {
      ShellToolDisabled: false,
      Connectors: [],
      MaxContextTokens: 0,
      MaxTurns: 0,
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
});
