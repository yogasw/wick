import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import LiveModelsPanel from "../LiveModelsPanel.svelte";
import * as api from "$lib/api.js";

vi.mock("$lib/api.js", async (importOriginal) => {
  const orig = await importOriginal<typeof import("$lib/api.js")>();
  return { ...orig, apiGetCLIModels: vi.fn(), apiGetEffectiveLiveModels: vi.fn(), apiRecheckCLIModel: vi.fn() };
});

const models = [
  { id: "opencode/big-pickle" },
  { id: "openai/gpt-5.5" },
  { id: "openai/gpt-5.5-mini" },
  { id: "anthropic/claude-sonnet" },
  { id: "google/gemini-3" },
];

function mockList(hostedAllowed: boolean) {
  vi.mocked(api.apiGetCLIModels).mockResolvedValue({
    models, hostedAllowed, fetchedAt: "2026-09-29T00:00:00Z",
  });
}

function renderPanel(props: Partial<{ filter: string; pin: string; type: string }> = {}) {
  const onSaveFilter = vi.fn();
  const onSavePin = vi.fn();
  render(LiveModelsPanel, {
    props: { base: "", type: props.type ?? "opencode", name: "oc", filter: props.filter ?? "", pin: props.pin ?? "", onSaveFilter, onSavePin },
  });
  return { onSaveFilter, onSavePin };
}

describe("LiveModelsPanel", () => {
  beforeEach(() => {
    vi.mocked(api.apiGetCLIModels).mockReset();
    vi.mocked(api.apiGetEffectiveLiveModels).mockReset().mockResolvedValue([]);
  });

  it("hides hosted opencode models unless allowed and counts matches", async () => {
    mockList(false);
    renderPanel();
    await waitFor(() => expect(screen.getByTestId("live-models-count").textContent).toMatch(/4\s+of 4 models match/));
    expect(screen.getByTestId("live-models-count").textContent).toContain("1 hosted opencode models hidden");
    expect(screen.getByTestId("live-models-list").textContent).not.toContain("opencode/big-pickle");
  });

  it("previews the unsaved filter with the shared grammar and saves it", async () => {
    mockList(true);
    const { onSaveFilter } = renderPanel();
    await waitFor(() => expect(screen.getByTestId("live-models-list")).toBeTruthy());
    const input = screen.getByTestId("live-models-filter") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "claude|gpt !mini" } });
    const list = screen.getByTestId("live-models-list").textContent ?? "";
    expect(list).toContain("openai/gpt-5.5");
    expect(list).toContain("anthropic/claude-sonnet");
    expect(list).not.toContain("gpt-5.5-mini");
    expect(list).not.toContain("gemini");
    // first match is the default when nothing is pinned
    expect(screen.getByTestId("live-models-count").textContent).toMatch(/2\s+of 5/);
    await fireEvent.click(screen.getByTestId("live-models-save-filter"));
    expect(onSaveFilter).toHaveBeenCalledWith("claude|gpt !mini");
  });

  it("marks the pinned model as default and refreshes on demand", async () => {
    mockList(true);
    renderPanel({ pin: "anthropic/claude-sonnet" });
    await waitFor(() => expect(screen.getByTestId("live-models-list").textContent).toMatch(/claude-sonnet\s*default/));
    await fireEvent.click(screen.getByTestId("live-models-refresh"));
    expect(api.apiGetCLIModels).toHaveBeenLastCalledWith("", "opencode", "oc", true);
  });

  it("shows the refresh error over a stale list", async () => {
    vi.mocked(api.apiGetCLIModels).mockResolvedValue({ models, hostedAllowed: true, fetchedAt: "", error: "opencode models: boom" });
    renderPanel();
    await waitFor(() => expect(screen.getByTestId("live-models-error").textContent).toContain("boom"));
  });

  it("uses the server's effective list: a refused model is marked, never the default, and can be re-checked", async () => {
    const offered = [
      { id: "openai-codex/gpt-5.5", unavailable: true },
      { id: "openai-codex/gpt-5.6-luna", default: true },
      { id: "openai-codex/gpt-5.4-mini" },
    ];
    vi.mocked(api.apiGetCLIModels).mockResolvedValue({
      models: offered, hostedAllowed: true, fetchedAt: "2026-10-01T00:00:00Z",
    });
    // The effective list is the picker endpoint's, as for wick live sets.
    vi.mocked(api.apiGetEffectiveLiveModels).mockResolvedValue(offered);
    vi.mocked(api.apiRecheckCLIModel).mockResolvedValue(true);
    renderPanel({ type: "omp", pin: "openai-codex/gpt-5.5" });
    await waitFor(() => expect(screen.getByTestId("live-models-list").textContent).toContain("not available for this account"));
    const list = screen.getByTestId("live-models-list");
    const refused = list.querySelector('[data-unavailable="true"]');
    expect(refused?.textContent).toContain("openai-codex/gpt-5.5");
    expect(refused?.textContent).not.toContain("default");
    expect(list.textContent).toMatch(/gpt-5\.6-luna\s*default/);
    // The pinned Default is the refused model: say so, use the effective one.
    expect(screen.getByTestId("live-models-pin-unavailable").textContent).toContain("using openai-codex/gpt-5.6-luna");
    await fireEvent.click(screen.getByTestId("live-models-recheck"));
    await waitFor(() => expect(api.apiRecheckCLIModel).toHaveBeenCalledWith("", "omp", "oc", "openai-codex/gpt-5.5"));
    expect(api.apiGetEffectiveLiveModels).toHaveBeenCalledWith("", "omp", "oc");
  });

  it("opening only reads what the server knows — the CLI runs on Refresh alone", async () => {
    vi.mocked(api.apiGetCLIModels).mockResolvedValue({ models: [{ id: "openai/old" }], hostedAllowed: true, fetchedAt: "2026-10-01T00:00:00Z", source: "files" });
    renderPanel({ type: "omp" });
    await waitFor(() => expect(screen.getByTestId("live-models-updated").textContent).toContain("files"));
    await new Promise((r) => setTimeout(r, 20));
    expect(api.apiGetCLIModels).toHaveBeenCalledTimes(1);
    expect(api.apiGetCLIModels).toHaveBeenCalledWith("", "omp", "oc", false);
  });

  it("a saved filter reads the server's list again instead of showing the old filter's rows", async () => {
    vi.mocked(api.apiGetCLIModels).mockResolvedValue({ models, hostedAllowed: true, fetchedAt: "2026-09-29T00:00:00Z" });
    // The server answers for whatever filter is saved at the time.
    let saved = "gpt";
    vi.mocked(api.apiGetEffectiveLiveModels).mockImplementation(async () =>
      saved === "gpt" ? [{ id: "openai/gpt-5.5", default: true }] : [{ id: "anthropic/claude-sonnet", default: true }],
    );
    const props = { base: "", type: "omp", name: "oc", filter: "gpt", pin: "", onSaveFilter: vi.fn(), onSavePin: vi.fn() };
    const { rerender } = render(LiveModelsPanel, { props });
    await waitFor(() => expect(screen.getByTestId("live-models-list").textContent).toContain("openai/gpt-5.5"));
    await new Promise((r) => setTimeout(r, 20));
    saved = "claude";
    await rerender({ ...props, filter: "claude" });
    await waitFor(() => expect(screen.getByTestId("live-models-list").textContent).toContain("anthropic/claude-sonnet"));
    expect(screen.getByTestId("live-models-list").textContent).not.toContain("openai/gpt-5.5");
  });

  it("says so when nothing is known yet", async () => {
    vi.mocked(api.apiGetCLIModels).mockResolvedValue({ models: [], hostedAllowed: true, fetchedAt: "" });
    renderPanel({ type: "omp" });
    await waitFor(() => expect(screen.getByTestId("live-models-empty").textContent).toContain("click Refresh"));
  });
});
