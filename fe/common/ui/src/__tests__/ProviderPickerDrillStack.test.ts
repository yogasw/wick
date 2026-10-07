import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ProviderPicker from "../ProviderPicker.svelte";
import type { ComposerModelOption } from "../composer-types.js";
import { decodePin, encodePath, encodePin } from "../model-path.js";

const OMP = { value: "omp/yoga", label: "omp/yoga", models: [] as ComposerModelOption[] };

// Instance → Provider → Account → Model, served by entry path.
const levels: Record<string, ComposerModelOption[]> = {
  "": [
    { id: "openai-codex", label: "openai-codex", default: true, live: true },
    { id: "anthropic", label: "anthropic", default: false, live: true },
  ],
  "openai-codex": [
    { id: "auto", label: "Auto (rotation)", default: true, live: true },
    { id: "2", label: "akun B", default: false, live: true },
  ],
  "openai-codex/2": [
    { id: "openai-codex/gpt-5.5", label: "gpt-5.5", default: false, unavailable: true },
    { id: "openai-codex/gpt-5.4", label: "gpt-5.4", default: true },
  ],
};
const loadModels = () =>
  vi.fn(async (_v: string, opts?: { entry?: string }) => levels[opts?.entry ?? ""] ?? []);

async function openAndDrill() {
  await fireEvent.click(screen.getByRole("button", { name: /select provider|omp/i }));
  await fireEvent.click(screen.getByRole("button", { name: /omp\/yoga/i }));
}

describe("model path grammar", () => {
  test("round-trips escaped segments; the first @ splits", () => {
    const path = ["openai-codex", "a@b/c d"];
    expect(encodePath(path)).toBe("openai-codex/a%40b%2Fc%20d");
    expect(decodePin(encodePin(path, "x/y@z"))).toEqual({ path, model: "x/y@z", grouped: true });
  });
  test("wick's legacy entry@model is a one-segment path", () => {
    expect(decodePin("set1@models/gemini-3-pro")).toEqual({ path: ["set1"], model: "models/gemini-3-pro", grouped: true });
    expect(decodePin("m1").grouped).toBe(false);
  });
});

describe("ProviderPicker drill stack", () => {
  test("drills any depth and pins <path>@<model>", async () => {
    const onChange = vi.fn();
    const lm = loadModels();
    render(ProviderPicker, { options: [OMP], value: "", onChange, loadModels: lm });

    await openAndDrill();
    await fireEvent.click(await screen.findByText("openai-codex"));
    expect(lm).toHaveBeenLastCalledWith("omp/yoga", { entry: "openai-codex" });
    await fireEvent.click(await screen.findByText("akun B"));
    expect(lm).toHaveBeenLastCalledWith("omp/yoga", { entry: "openai-codex/2" });
    // Breadcrumb names every level.
    expect(screen.getByText("omp/yoga · openai-codex · akun B")).toBeTruthy();
    await fireEvent.click(await screen.findByText("gpt-5.4"));
    expect(onChange).toHaveBeenCalledWith("omp/yoga::openai-codex/2@openai-codex/gpt-5.4");
  });

  test("an unavailable model is greyed out and cannot be picked", async () => {
    const onChange = vi.fn();
    render(ProviderPicker, { options: [OMP], value: "", onChange, loadModels: loadModels() });
    await openAndDrill();
    await fireEvent.click(await screen.findByText("openai-codex"));
    await fireEvent.click(await screen.findByText("akun B"));
    const row = (await screen.findByText("gpt-5.5")).closest("button")!;
    expect(row.hasAttribute("disabled")).toBe(true);
    await fireEvent.click(row);
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText("unavailable")).toBeTruthy();
  });

  test("back pops one level at a time", async () => {
    render(ProviderPicker, { options: [OMP], value: "", onChange: vi.fn(), loadModels: loadModels() });
    await openAndDrill();
    await fireEvent.click(await screen.findByText("openai-codex"));
    await fireEvent.click(await screen.findByText("akun B"));
    await fireEvent.click(screen.getByText("omp/yoga · openai-codex · akun B"));
    expect(await screen.findByText("Auto (rotation)")).toBeTruthy();
    await fireEvent.click(screen.getByText("omp/yoga · openai-codex"));
    expect(await screen.findByText("anthropic")).toBeTruthy();
  });
});

describe("ProviderPicker last updated + Refresh", () => {
  test("shows the server's stamp and Refresh asks for a CLI run once", async () => {
    const { withModelListMeta } = await import("../model-list-meta.js");
    const lm = vi.fn(async (_v: string, opts?: { entry?: string; refresh?: boolean }) =>
      withModelListMeta(
        [{ id: "openai-codex/gpt-5.6-luna", label: "gpt-5.6-luna", default: true }],
        { fetched_at: "2026-10-01T00:55:00Z", source: opts?.refresh ? "cli" : "files", can_refresh: true },
      ),
    );
    render(ProviderPicker, { options: [OMP], value: "", onChange: vi.fn(), loadModels: lm });
    await openAndDrill();
    const line = await screen.findByTestId("picker-models-updated");
    expect(line.textContent).toContain("files");
    await fireEvent.click(screen.getByTestId("picker-models-refresh"));
    expect(lm).toHaveBeenLastCalledWith("omp/yoga", { refresh: true });
    await vi.waitFor(() => expect(screen.getByTestId("picker-models-updated").textContent).toContain("cli"));
  });

  test("a Refresh that comes back empty clears the old rows", async () => {
    const { withModelListMeta } = await import("../model-list-meta.js");
    const lm = vi.fn(async (_v: string, opts?: { entry?: string; refresh?: boolean }) =>
      withModelListMeta(
        opts?.refresh ? [] : [{ id: "openai-codex/gpt-5.6-luna", label: "gpt-5.6-luna", default: true }],
        { fetched_at: "2026-10-01T00:55:00Z", source: opts?.refresh ? "cli" : "files", can_refresh: true },
      ),
    );
    render(ProviderPicker, { options: [OMP], value: "", onChange: vi.fn(), loadModels: lm });
    await openAndDrill();
    expect(await screen.findByText("gpt-5.6-luna")).toBeTruthy();
    await fireEvent.click(screen.getByTestId("picker-models-refresh"));
    await vi.waitFor(() => expect(screen.queryByText("gpt-5.6-luna")).toBeNull());
  });
});
