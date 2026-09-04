import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ProviderPicker from "../ProviderPicker.svelte";
import type { ComposerModelOption } from "../composer-types.js";

const WICK = { value: "wick/x", label: "wick/x", models: [] as ComposerModelOption[] };

/** Open the dropdown, then drill into the wick instance. */
async function openAndDrill() {
  await fireEvent.click(screen.getByRole("button", { name: /select provider|wick/i }));
  await fireEvent.click(screen.getByRole("button", { name: /wick\/x/i }));
}

describe("ProviderPicker live sets", () => {
  // Without a loader the picker can only offer models baked into `options`,
  // which for wick means it never reaches a live set's leaves.
  test("fetches an instance's models lazily when drilled into", async () => {
    const loadModels = vi.fn().mockResolvedValue([
      { id: "m1", label: "Model One", default: true },
    ] satisfies ComposerModelOption[]);
    render(ProviderPicker, { options: [WICK], value: "", onChange: vi.fn(), loadModels });

    await openAndDrill();
    expect(loadModels).toHaveBeenCalledWith("wick/x", undefined);
    expect(await screen.findByText("Model One")).toBeTruthy();
  });

  // A live-set row names a SET, so clicking it must expand rather than
  // commit — committing would store an entry id with no vendor model behind
  // it, which is exactly what fails at spawn with "no runnable model".
  test("expands a live-set row instead of selecting it", async () => {
    const onChange = vi.fn();
    const loadModels = vi.fn(async (_v: string, opts?: { entry?: string }) =>
      opts?.entry
        ? [{ id: "gemini-3-pro", label: "Gemini 3 Pro", default: true }]
        : [{ id: "set1", label: "Gemini (live)", default: false, live: true }],
    );
    render(ProviderPicker, { options: [WICK], value: "", onChange, loadModels });

    await openAndDrill();
    const setRow = await screen.findByText("Gemini (live)");
    await fireEvent.click(setRow);

    expect(onChange).not.toHaveBeenCalled();
    expect(loadModels).toHaveBeenLastCalledWith("wick/x", { entry: "set1" });
    expect(await screen.findByText("Gemini 3 Pro")).toBeTruthy();
  });

  // The leaf is addressed as "<entryID>@<vendorModelID>": the entry supplies
  // the credentials and base URL, the vendor id the concrete model.
  test("packs a chosen leaf as entry@vendorModel", async () => {
    const onChange = vi.fn();
    const loadModels = vi.fn(async (_v: string, opts?: { entry?: string }) =>
      opts?.entry
        ? [{ id: "gemini-3-pro", label: "Gemini 3 Pro", default: true }]
        : [{ id: "set1", label: "Gemini (live)", default: false, live: true }],
    );
    render(ProviderPicker, { options: [WICK], value: "", onChange, loadModels });

    await openAndDrill();
    await fireEvent.click(await screen.findByText("Gemini (live)"));
    await fireEvent.click(await screen.findByText("Gemini 3 Pro"));

    expect(onChange).toHaveBeenCalledWith("wick/x::set1@gemini-3-pro");
  });

  // A plain model keeps its bare id — only live-set leaves carry the "@".
  test("packs a plain model as its own id", async () => {
    const onChange = vi.fn();
    const loadModels = vi.fn().mockResolvedValue([{ id: "m1", label: "Model One", default: true }]);
    render(ProviderPicker, { options: [WICK], value: "", onChange, loadModels });

    await openAndDrill();
    await fireEvent.click(await screen.findByText("Model One"));

    expect(onChange).toHaveBeenCalledWith("wick/x::m1");
  });

  // The popup must be fixed-positioned, not absolute: inside a Modal (whose
  // body is overflow-auto) an absolute popup is clipped by the body and makes
  // the dialog scroll instead of painting over it.
  test("renders the menu fixed so no overflow ancestor can clip it", async () => {
    render(ProviderPicker, { options: [WICK], value: "", onChange: vi.fn() });
    await fireEvent.click(screen.getByRole("button", { name: /select provider/i }));

    // Read the resolved style property, not the raw attribute text: the DOM
    // normalizes "position:fixed" to "position: fixed".
    const menu = Array.from(document.querySelectorAll<HTMLElement>("div[style]")).find(
      (d) => d.style.position === "fixed",
    );
    expect(menu).toBeTruthy();
    expect(menu?.style.zIndex).toBe("9999");
  });

  // The menu is no longer a DOM child of the trigger's wrapper, so the
  // outside-click handler has to know about it explicitly or the first click
  // on any row would close the menu instead of selecting.
  test("a click inside the menu does not close it", async () => {
    const onChange = vi.fn();
    const loadModels = vi.fn().mockResolvedValue([
      { id: "m1", label: "Model One", default: true },
      { id: "m2", label: "Model Two", default: false },
    ]);
    render(ProviderPicker, { options: [WICK], value: "", onChange, loadModels });

    await openAndDrill();
    const row = await screen.findByText("Model One");
    await fireEvent.mouseDown(row);
    // Still open: the filter/rows are reachable after a mousedown inside.
    expect(screen.queryByText("Model Two")).toBeTruthy();
  });

  // The provider list collapses a single-model instance to no models at all,
  // so a stored wick id has nothing static to name it and the closed trigger
  // would read "wick/x · m_0370951f-68d".
  test("names an id-shaped pin on the closed trigger via the loader", async () => {
    const loadModels = vi
      .fn()
      .mockResolvedValue([{ id: "m_0370951f-68d", label: "Grok 4.5", default: true }]);
    render(ProviderPicker, {
      options: [WICK],
      value: "wick/x::m_0370951f-68d",
      onChange: vi.fn(),
      loadModels,
    });
    expect(await screen.findByRole("button", { name: /Grok 4\.5/ })).toBeTruthy();
  });

  // An instance whose loader returns nothing (no models activated, vendor
  // unreachable) must still be selectable: without an escape row the drill
  // shows "No models available" and the provider can never be chosen at all.
  test("an empty model list still lets the instance be picked without a pin", async () => {
    const onChange = vi.fn();
    const loadModels = vi.fn().mockResolvedValue([]);
    render(ProviderPicker, { options: [WICK], value: "", onChange, loadModels });

    await openAndDrill();
    await fireEvent.click(await screen.findByText(/use default model/i));

    expect(onChange).toHaveBeenCalledWith("wick/x");
  });

  // A live-set pin matches no entry in the static list, so showing the bare
  // instance name would read as "no model chosen" for a project that pinned
  // a specific leaf.
  test("labels a live-set pin with its vendor model", () => {
    render(ProviderPicker, {
      options: [WICK],
      value: "wick/x::set1@gemini-3-pro",
      onChange: vi.fn(),
    });
    expect(screen.getByRole("button", { name: /gemini-3-pro/ })).toBeTruthy();
  });
});
