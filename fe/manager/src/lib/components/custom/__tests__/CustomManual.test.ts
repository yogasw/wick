import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import CustomManual from "../CustomManual.svelte";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));

beforeEach(() => {
  vi.clearAllMocks();
  sessionStorage.clear();
  vi.mocked(api.getCustomMeta).mockResolvedValue({ ai_providers: [], categories: ["API"] });
});

describe("CustomManual stepper", () => {
  it("starts on the Meta step with a Next button", async () => {
    render(CustomManual);
    expect(await screen.findByText("Build a connector by hand")).toBeTruthy();
    expect(screen.getByLabelText("Key")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Next →" })).toBeTruthy();
    /* Ops section is hidden on step 1 */
    expect(screen.queryByRole("button", { name: "+ Add operation" })).toBeNull();
  });

  it("advances to Configs then Operations", async () => {
    render(CustomManual);
    await screen.findByText("Build a connector by hand");
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    expect(await screen.findByRole("button", { name: "+ Add field" })).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Step 3 — Operations →" }));
    expect(await screen.findByRole("button", { name: "+ Add operation" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Save connector →" })).toBeTruthy();
  });

  it("goes back from a later step", async () => {
    render(CustomManual);
    await screen.findByText("Build a connector by hand");
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    await screen.findByRole("button", { name: "+ Add field" });
    await fireEvent.click(screen.getByRole("button", { name: "← Back" }));
    expect(await screen.findByLabelText("Key")).toBeTruthy();
  });

  it("saves the manual draft on the last step", async () => {
    vi.mocked(api.saveCustomDraft).mockResolvedValue({ redirect: "" });
    render(CustomManual);
    await screen.findByText("Build a connector by hand");
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    await fireEvent.click(await screen.findByRole("button", { name: "Step 3 — Operations →" }));
    await fireEvent.click(await screen.findByRole("button", { name: "Save connector →" }));
    await vi.waitFor(() => expect(api.saveCustomDraft).toHaveBeenCalled());
    expect(vi.mocked(api.saveCustomDraft).mock.calls[0][0].source).toBe("manual");
  });

  it("does not navigate via the router push for in-page steps", async () => {
    render(CustomManual);
    await screen.findByText("Build a connector by hand");
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    expect(router.push).not.toHaveBeenCalled();
  });
});
