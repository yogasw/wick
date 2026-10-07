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

  it("shows AI-error guidance paragraph on parse error", async () => {
    vi.mocked(api.parseCustomPaste).mockRejectedValue(new Error("bad paste"));
    render(CustomPaste);
    await screen.findByText("New connector from paste");
    await fireEvent.input(screen.getByLabelText("Paste box"), { target: { value: "something" } });
    await fireEvent.click(screen.getByRole("button", { name: "Parse →" }));
    expect(await screen.findByText(/Common causes:/)).toBeTruthy();
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

describe("CustomPaste AI tab", () => {
  it("queues a connector-parse job and opens review with its draft", async () => {
    vi.mocked(api.getCustomMeta).mockResolvedValue({ ai_providers: ["claude"], categories: [] });
    const draft = { key: "items", name: "Items", ops: [] };
    const fetchMock = vi.fn(async (url: string) => {
      if (url === "/api/ai-gen") {
        return new Response(JSON.stringify({ id: "gen_1", kind: "connector-parse", status: "queued", position: 1 }), { status: 202 });
      }
      return new Response(JSON.stringify({ id: "gen_1", kind: "connector-parse", status: "done", result: draft }), { status: 200 });
    });
    vi.stubGlobal("fetch", fetchMock);
    try {
      render(CustomPaste);
      await fireEvent.click(await screen.findByRole("button", { name: "✨ AI parser" }));
      await fireEvent.input(screen.getByLabelText("Paste box"), { target: { value: "fetch('https://x')" } });
      await fireEvent.click(screen.getByRole("button", { name: "Parse →" }));
      await vi.waitFor(() => expect(screen.getByRole("status").textContent).toContain("Queued · #1"));
      await vi.waitFor(() => expect(router.push).toHaveBeenCalledWith("/custom/review"), { timeout: 3000 });
      const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
      expect(JSON.parse(String(init.body))).toEqual({
        kind: "connector-parse",
        input: { text: "fetch('https://x')", provider: "claude" },
      });
      expect(api.parseCustomPaste).not.toHaveBeenCalled();
      expect(JSON.parse(sessionStorage.getItem(DRAFT_STORAGE_KEY) ?? "{}").key).toBe("items");
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("puts a failed AI job in the error box", async () => {
    vi.mocked(api.getCustomMeta).mockResolvedValue({ ai_providers: ["claude"], categories: [] });
    vi.stubGlobal("fetch", vi.fn(async () =>
      new Response(JSON.stringify({ error: "paste is larger than 8 KB — trim it down to a single endpoint" }), { status: 422 })));
    try {
      render(CustomPaste);
      await fireEvent.click(await screen.findByRole("button", { name: "✨ AI parser" }));
      await fireEvent.input(screen.getByLabelText("Paste box"), { target: { value: "x" } });
      await fireEvent.click(screen.getByRole("button", { name: "Parse →" }));
      expect(await screen.findByText(/larger than 8 KB/)).toBeTruthy();
      expect(screen.getByText(/Common causes:/)).toBeTruthy();
      expect(router.push).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
