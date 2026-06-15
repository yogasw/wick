import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import CustomPaste from "../CustomPaste.svelte";
import { DRAFT_STORAGE_KEY } from "../storage.js";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));

beforeEach(() => {
  vi.clearAllMocks();
  sessionStorage.clear();
  vi.mocked(api.getCustomMeta).mockResolvedValue({ ai_providers: [], categories: ["API"] });
});

describe("CustomPaste", () => {
  it("renders the cURL tab and paste box", async () => {
    render(CustomPaste);
    expect(await screen.findByText("New connector from paste")).toBeTruthy();
    expect(screen.getByRole("button", { name: "📋 cURL parser" })).toBeTruthy();
    expect(screen.getByLabelText("Paste box")).toBeTruthy();
  });

  it("hides the AI tab when no providers are configured", async () => {
    render(CustomPaste);
    await screen.findByText("New connector from paste");
    expect(screen.queryByRole("button", { name: "✨ AI parser" })).toBeNull();
  });

  it("shows the AI tab and provider picker when providers exist", async () => {
    vi.mocked(api.getCustomMeta).mockResolvedValue({ ai_providers: ["openai"], categories: [] });
    render(CustomPaste);
    await fireEvent.click(await screen.findByRole("button", { name: "✨ AI parser" }));
    expect(screen.getByText("Provider")).toBeTruthy();
  });

  it("errors on empty paste without calling the API", async () => {
    render(CustomPaste);
    await screen.findByText("New connector from paste");
    await fireEvent.click(screen.getByRole("button", { name: "Parse →" }));
    expect(await screen.findByText(/Paste something first\./)).toBeTruthy();
    expect(api.parseCustomPaste).not.toHaveBeenCalled();
  });

  it("parses, stores the draft, and navigates to review", async () => {
    vi.mocked(api.parseCustomPaste).mockResolvedValue({
      key: "petstore",
      name: "Petstore",
      description: "",
      icon: "🔌",
      source: "curl",
      category: "",
      single: false,
      allow_session_config: false,
      health_op: "",
      health_expect: "",
      configs: [],
      ops: [],
    });
    render(CustomPaste);
    await screen.findByText("New connector from paste");
    await fireEvent.input(screen.getByLabelText("Paste box"), { target: { value: "curl https://x" } });
    await fireEvent.click(screen.getByRole("button", { name: "Parse →" }));

    expect(await vi.waitFor(() => router.push)).toBeTruthy();
    expect(api.parseCustomPaste).toHaveBeenCalledWith("curl", "", "curl https://x");
    expect(router.push).toHaveBeenCalledWith("/custom/review");
    const stored = JSON.parse(sessionStorage.getItem(DRAFT_STORAGE_KEY) ?? "{}");
    expect(stored.key).toBe("petstore");
  });

  it("surfaces a parse error", async () => {
    vi.mocked(api.parseCustomPaste).mockRejectedValue(new Error("could not parse"));
    render(CustomPaste);
    await screen.findByText("New connector from paste");
    await fireEvent.input(screen.getByLabelText("Paste box"), { target: { value: "garbage" } });
    await fireEvent.click(screen.getByRole("button", { name: "Parse →" }));
    expect(await screen.findByText(/could not parse/)).toBeTruthy();
    expect(router.push).not.toHaveBeenCalled();
  });
});
