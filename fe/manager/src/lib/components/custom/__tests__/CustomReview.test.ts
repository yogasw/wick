import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import CustomReview from "../CustomReview.svelte";
import { DRAFT_STORAGE_KEY } from "../storage.js";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";
import type { Draft } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));

function makeDraft(over: Partial<Draft> = {}): Draft {
  return {
    key: "petstore",
    name: "Petstore",
    description: "A pet store",
    icon: "🐾",
    source: "curl",
    category: "API",
    single: false,
    allow_session_config: false,
    health_op: "",
    health_expect: "",
    configs: [{ key: "base_url", widget: "url", options: "", secret: false, required: true, default: "", desc: "" }],
    ops: [{ key: "list", name: "List", description: "", destructive: false, inputs: [], request: { method: "GET", url_template: "/p", headers: {}, body_template: "", content_type: "" } }],
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  sessionStorage.clear();
  vi.mocked(api.getCustomMeta).mockResolvedValue({ ai_providers: [], categories: ["API"] });
});

describe("CustomReview — new mode", () => {
  it("loads the draft from sessionStorage and shows the create toolbar", async () => {
    sessionStorage.setItem(DRAFT_STORAGE_KEY, JSON.stringify(makeDraft()));
    render(CustomReview);
    expect(await screen.findByText("Review extracted definition")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Save connector →" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Delete" })).toBeNull();
  });

  it("saves a new draft and clears the hand-off", async () => {
    sessionStorage.setItem(DRAFT_STORAGE_KEY, JSON.stringify(makeDraft()));
    vi.mocked(api.saveCustomDraft).mockResolvedValue({ redirect: "" });
    render(CustomReview);
    await screen.findByText("Review extracted definition");
    await fireEvent.click(screen.getByRole("button", { name: "Save connector →" }));
    await vi.waitFor(() => expect(api.saveCustomDraft).toHaveBeenCalled());
    const payload = vi.mocked(api.saveCustomDraft).mock.calls[0][0];
    expect(payload.key).toBe("petstore");
    expect(sessionStorage.getItem(DRAFT_STORAGE_KEY)).toBeNull();
  });
});

describe("CustomReview — edit mode", () => {
  it("loads the draft via the draft endpoint and shows edit actions", async () => {
    vi.mocked(api.getCustomDraft).mockResolvedValue({ def_id: "def-1", disabled: false, mcp: false, draft: makeDraft() });
    render(CustomReview, { defID: "def-1" });
    expect(await screen.findByText("Edit connector definition")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Save changes" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Delete" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Disable" })).toBeTruthy();
    expect(api.getCustomDraft).toHaveBeenCalledWith("def-1");
  });

  it("updates the draft on save", async () => {
    vi.mocked(api.getCustomDraft).mockResolvedValue({ def_id: "def-1", disabled: false, mcp: false, draft: makeDraft() });
    vi.mocked(api.updateCustomDraft).mockResolvedValue({ ok: true });
    render(CustomReview, { defID: "def-1" });
    await screen.findByText("Edit connector definition");
    await fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await vi.waitFor(() => expect(api.updateCustomDraft).toHaveBeenCalled());
    expect(vi.mocked(api.updateCustomDraft).mock.calls[0][0]).toBe("def-1");
  });

  it("deletes after confirmation and navigates home", async () => {
    vi.mocked(api.getCustomDraft).mockResolvedValue({ def_id: "def-1", disabled: false, mcp: false, draft: makeDraft() });
    vi.mocked(api.deleteCustomDef).mockResolvedValue(undefined);
    render(CustomReview, { defID: "def-1" });
    await screen.findByText("Edit connector definition");
    /* Toolbar Delete opens the dialog. */
    await fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    /* Now two Delete buttons exist (toolbar + dialog); confirm via the last. */
    const deletes = await screen.findAllByRole("button", { name: "Delete" });
    await fireEvent.click(deletes[deletes.length - 1]);
    await vi.waitFor(() => expect(api.deleteCustomDef).toHaveBeenCalledWith("def-1"));
    expect(router.push).toHaveBeenCalledWith("/");
  });

  it("toggles disabled state", async () => {
    vi.mocked(api.getCustomDraft).mockResolvedValue({ def_id: "def-1", disabled: false, mcp: false, draft: makeDraft() });
    vi.mocked(api.setCustomDefDisabled).mockResolvedValue(true);
    render(CustomReview, { defID: "def-1" });
    await screen.findByText("Edit connector definition");
    await fireEvent.click(screen.getByRole("button", { name: "Disable" }));
    await vi.waitFor(() => expect(api.setCustomDefDisabled).toHaveBeenCalledWith("def-1", true));
    expect(await screen.findByRole("button", { name: "Enable" })).toBeTruthy();
  });

  it("adds a config row through the editor", async () => {
    vi.mocked(api.getCustomDraft).mockResolvedValue({ def_id: "def-1", disabled: false, mcp: false, draft: makeDraft({ configs: [], ops: [] }) });
    render(CustomReview, { defID: "def-1" });
    await screen.findByText("Edit connector definition");
    expect(screen.queryByLabelText("Field key")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "+ Add field" }));
    expect(await screen.findByLabelText("Field key")).toBeTruthy();
  });
});
