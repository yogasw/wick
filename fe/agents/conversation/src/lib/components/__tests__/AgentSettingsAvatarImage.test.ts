import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";

/* Answers like the server, not an echo: an "image" avatar without a
   stored picture comes back Classic, and the picture gains its src. An
   echo hid the autosave loop (review B1). */
type Av = { kind?: string; shape: string; color: string; image?: Record<string, unknown> };
/** The stored picture's version when another tab replaced it ("" = as sent). */
let storedV = "";
function serverAvatar(a: Av): Av {
  const out: Av = { ...a };
  if (out.image) {
    if (typeof out.image.v !== "string") delete out.image;
    else {
      const v = storedV || out.image.v;
      out.image = { ...out.image, v, src: `/tools/agents/api/team/agents/a1/avatar/image?v=${v}` };
    }
  }
  if (out.kind === "image" && !out.image) delete out.kind;
  return out;
}
const updateAgent = vi.fn((_b: string, _id: string, body: unknown) => {
  const b = body as { avatar?: Av };
  return Promise.resolve({ ...agent, ...b, ...(b.avatar ? { avatar: serverAvatar(b.avatar) } : {}) });
});
const uploadAvatarImage = vi.fn((_b: string, _id: string, _data: string) => Promise.resolve({ v: "0123456789abcdef", aspect: 2, width: 320, height: 160 }));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  updateAgent: (b: string, id: string, body: unknown) => updateAgent(b, id, body),
  uploadAvatarImage: (b: string, id: string, data: string) => uploadAvatarImage(b, id, data),
  listAgentConnectors: () => Promise.resolve([]),
  getAgent: () => Promise.resolve(null),
  getProjectPersona: () => Promise.resolve(null),
  runApi: <T,>(p: Promise<T>) => p,
}));
// jsdom's File has no arrayBuffer: the base64 step is stubbed.
vi.mock("../../avatarImage.js", async (orig) => ({
  ...(await orig<typeof import("../../avatarImage.js")>()),
  fileData: () => Promise.resolve("iVBORw=="),
}));
vi.mock("../../api/options.js", () => ({
  getProviderOptions: () => Promise.resolve([]),
  getProjectOptions: () => Promise.resolve([]),
}));

import AgentSettings from "../AgentSettings.svelte";
import AvatarEventsTable from "../AvatarEventsTable.svelte";
import type { AgentItem } from "../../api/team.js";

const agent = {
  id: "a1", handle: "worker", is_captain: false, project_id: "p1", name: "Worker", icon: "",
  description: "", system_prompt: "", provider: "", model: "", preset: "",
  features: {}, avatar: { shape: "circle", color: "#27b199" }, allowed_connectors: [],
  include_new_connectors: false, disabled: false, main_session_id: "", last_active: null,
  last_preview: "", status: "idle",
} as unknown as AgentItem;
const imgAgent = {
  ...agent,
  avatar: { kind: "image", shape: "circle", color: "#27b199", image: { v: "0123456789abcdef", aspect: 2, fit: "fill", x: 0, y: 0, zoom: 0, src: "/tools/agents/api/team/agents/a1/avatar/image?v=0123456789abcdef" } },
} as unknown as AgentItem;

const props = (a: AgentItem) => ({ base: "/tools/agents", agent: a, agents: [a], tab: "avatar" as const, onTab: vi.fn(), onClose: vi.fn(), onSaved: vi.fn(), onDeleted: vi.fn() });
const lastAvatar = () => (updateAgent.mock.calls.at(-1)?.[2] as { avatar?: Record<string, unknown> } | undefined)?.avatar;

describe("AgentSettings avatar: image style", () => {
  beforeEach(() => {
    updateAgent.mockClear();
    uploadAvatarImage.mockClear();
    storedV = "";
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve({ ok: false, json: () => Promise.resolve({}) })));
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
  });

  test("the style picker has Classic, Blob and Image", () => {
    render(AgentSettings, { props: props(agent) });
    const group = screen.getByRole("group", { name: "Avatar kind" });
    expect([...group.querySelectorAll("button")].map((b) => b.textContent?.trim())).toEqual(["Classic", "Blob", "Image"]);
  });

  test("Image → upload stores the picture and saves the image avatar", async () => {
    render(AgentSettings, { props: props(agent) });
    await fireEvent.click(screen.getByTestId("avatar-kind-image"));
    expect(screen.getByTestId("avatar-image-editor")).toBeDefined();
    const file = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], "pic.png", { type: "image/png" });
    await fireEvent.change(screen.getByTestId("avatar-image-file"), { target: { files: [file] } });
    await waitFor(() => expect(uploadAvatarImage).toHaveBeenCalledWith("/tools/agents", "a1", "iVBORw=="));
    await waitFor(() => expect(lastAvatar()).toMatchObject({ kind: "image", image: { v: "0123456789abcdef", aspect: 2, fit: "fill", zoom: 0 } }));
    // The <img> only ever points at the app's own versioned endpoint.
    const img = screen.getByTestId("avatar-image-frame").querySelector("img");
    expect(img?.getAttribute("src")).toBe("/tools/agents/api/team/agents/a1/avatar/image?v=0123456789abcdef");
  });

  test("a new upload keeps the chosen crop shape (review M1)", async () => {
    const hexAgent = { ...imgAgent, avatar: { ...imgAgent.avatar, image: { ...imgAgent.avatar!.image!, crop: "hex", x: 20 } } } as unknown as AgentItem;
    render(AgentSettings, { props: props(hexAgent) });
    expect(screen.getByRole("button", { name: "Hexagon" }).getAttribute("aria-pressed")).toBe("true");
    const file = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], "pic.png", { type: "image/png" });
    await fireEvent.change(screen.getByTestId("avatar-image-file"), { target: { files: [file] } });
    await waitFor(() => expect(uploadAvatarImage).toHaveBeenCalled());
    await waitFor(() => expect(lastAvatar()).toMatchObject({ kind: "image", image: { aspect: 2, x: 0, crop: "hex" } }));
    expect(screen.getByRole("button", { name: "Hexagon" }).getAttribute("aria-pressed")).toBe("true");
  });

  test("a non-image file is refused before any upload", async () => {
    render(AgentSettings, { props: props(imgAgent) });
    const svg = new File(["<svg/>"], "x.svg", { type: "image/svg+xml" });
    await fireEvent.change(screen.getByTestId("avatar-image-file"), { target: { files: [svg] } });
    expect(uploadAvatarImage).not.toHaveBeenCalled();
    expect(screen.getByTestId("avatar-image-msg").textContent).toContain("PNG, JPEG or WebP");
  });

  test("crop controls: Fit, zoom slider, Fill", async () => {
    render(AgentSettings, { props: props(imgAgent) });
    await fireEvent.click(screen.getByTestId("avatar-image-fit"));
    await waitFor(() => expect(lastAvatar()?.image).toMatchObject({ fit: "fit", x: 0, y: 0, zoom: 0 }));
    await fireEvent.input(screen.getByTestId("avatar-image-zoom"), { target: { value: "9" } });
    await waitFor(() => expect((lastAvatar()?.image as { zoom: number }).zoom).toBe(5)); // clamped
    await fireEvent.click(screen.getByTestId("avatar-image-fill"));
    await waitFor(() => expect(lastAvatar()?.image).toMatchObject({ fit: "fill", zoom: 0 }));
    await fireEvent.keyDown(screen.getByTestId("avatar-image-frame"), { key: "ArrowRight", shiftKey: true });
    await waitFor(() => expect((lastAvatar()?.image as { x: number }).x).toBe(10));
  });

  const settle = () => new Promise((r) => setTimeout(r, 1200)); // > 2 autosave periods

  test("B1: Image with no picture saves nothing — no PATCH loop", async () => {
    render(AgentSettings, { props: props(agent) });
    await fireEvent.click(screen.getByTestId("avatar-kind-image"));
    await settle();
    expect(updateAgent).not.toHaveBeenCalled();
    expect(screen.getByText("No image yet")).toBeDefined();
  });

  test("B1/M2: Reset only clears locally — no PATCH, no delete", async () => {
    render(AgentSettings, { props: props(imgAgent) });
    await fireEvent.click(screen.getByTestId("avatar-image-reset"));
    await settle();
    expect(updateAgent).not.toHaveBeenCalled();
    expect(screen.getByText("No image yet")).toBeDefined();
  });

  test("the server's src does not make the draft look changed", async () => {
    render(AgentSettings, { props: props(imgAgent) });
    await settle();
    expect(updateAgent).not.toHaveBeenCalled();
  });

  test("N1: the server's v (picture replaced in another tab) is adopted — one PATCH, then quiet", async () => {
    storedV = "fedcba9876543210";
    render(AgentSettings, { props: props(imgAgent) });
    await fireEvent.click(screen.getByTestId("avatar-image-fit"));
    await waitFor(() => expect(updateAgent).toHaveBeenCalledTimes(1));
    await settle();
    expect(updateAgent).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId("avatar-image-frame").querySelector("img")?.getAttribute("src")).toContain("?v=fedcba9876543210");
  });

  test("N2: while Image is pending, other avatar edits still save under the saved style", async () => {
    render(AgentSettings, { props: props(agent) });
    await fireEvent.click(screen.getByTestId("avatar-kind-image"));
    expect(screen.getByTestId("avatar-image-pending")).toBeDefined();
    await fireEvent.click(screen.getByRole("switch", { name: "A tool failed on" }));
    await waitFor(() => expect(updateAgent).toHaveBeenCalledTimes(1));
    expect(lastAvatar()).toMatchObject({ shape: "circle", color: "#27b199", events: { error: { off: true } } });
    expect(lastAvatar()?.kind).toBeUndefined();
    expect(lastAvatar()?.image).toBeUndefined();
    await settle();
    expect(updateAgent).toHaveBeenCalledTimes(1);
    // The style stays Image locally, still waiting for an upload.
    expect(screen.getByTestId("avatar-image-pending")).toBeDefined();
  });

  test("M2: Classic keeps the crop, and Image restores the picture", async () => {
    render(AgentSettings, { props: props(imgAgent) });
    await fireEvent.click(screen.getByTestId("avatar-kind-classic"));
    await waitFor(() => expect(lastAvatar()).toMatchObject({ shape: "circle", image: { v: "0123456789abcdef" } }));
    expect(lastAvatar()?.kind).toBeUndefined();
    await settle();
    const calls = updateAgent.mock.calls.length;
    await fireEvent.click(screen.getByTestId("avatar-kind-image"));
    await waitFor(() => expect(lastAvatar()).toMatchObject({ kind: "image", image: { v: "0123456789abcdef" } }));
    expect(screen.getByTestId("avatar-image-frame").querySelector("img")?.getAttribute("src")).toContain("?v=0123456789abcdef");
    await settle();
    expect(updateAgent.mock.calls.length).toBe(calls + 1); // one save, no loop
  });
});

describe("Reacts to: previews animate", () => {
  test("classic rows run their motion loop (each frame redraws the body)", async () => {
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));
    const frames: FrameRequestCallback[] = [];
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => (frames.push(cb), frames.length));
    vi.stubGlobal("cancelAnimationFrame", () => {});
    render(AvatarEventsTable, { props: { shape: "circle", color: "#27b199", onChange: vi.fn() } });
    const path = () => screen.getByTestId("avatar-event-error").querySelector("svg.agent-avatar path")?.getAttribute("d");
    expect(frames.length).toBeGreaterThan(0); // a still row never subscribes
    const seen = new Set<string | null | undefined>();
    for (const ms of [1000, 1400, 1800, 2300]) {
      frames.splice(0).forEach((f) => f(ms));
      await tick();
      seen.add(path());
    }
    expect(seen.size).toBeGreaterThan(1);
    vi.unstubAllGlobals();
  });

  test("rows are live, never still (the bug: `still` froze every preview)", () => {
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));
    const image = { src: "/tools/agents/api/team/agents/a1/avatar/image?v=0123456789abcdef", aspect: 1, fit: "fill", x: 0, y: 0, zoom: 0 };
    render(AvatarEventsTable, { props: { kind: "image", shape: "circle", color: "#27b199", image, onChange: vi.fn() } });
    const rows = screen.getAllByTestId("image-avatar");
    expect(rows.length).toBe(9);
    for (const r of rows) expect(r.classList.contains("animate")).toBe(true);
  });

});

describe("AgentSettings avatar: Poses grid", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve({ ok: false, json: () => Promise.resolve({}) })));
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
  });

  test("Image shows the 13 poses, each tile drawing and labelling its own", () => {
    render(AgentSettings, { props: props(imgAgent) });
    const grid = screen.getByTestId("avatar-poses");
    expect(grid.textContent).toContain("Poses");
    expect(grid.textContent).toContain("Every pose has its own motion. Pick them in the table below.");
    const poses = ["idle", "wide", "scan", "thinking", "type", "orbit", "send", "wait", "alert", "fail", "notify", "swirl", "squeeze"];
    for (const p of poses) {
      const tile = screen.getByTestId(`avatar-pose-${p}`);
      expect(tile.lastElementChild?.textContent?.trim()).toBe(p);
      expect(tile.querySelector("[data-testid=image-avatar]")?.getAttribute("data-pose")).toBe(p);
    }
    expect(grid.querySelectorAll("[data-testid=image-avatar]").length).toBe(13);
    vi.unstubAllGlobals();
  });

  test("the Poses heading matches Reacts to", () => {
    render(AgentSettings, { props: props(imgAgent) });
    const h = screen.getByText("Poses", { selector: "[data-testid=avatar-poses] > p" });
    const r = screen.getByText("Reacts to");
    expect(h.className).toBe(r.className);
    vi.unstubAllGlobals();
  });

  test("under reduced motion the 13 tiles are still frames, marks kept", () => {
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: q.includes("reduce"), media: q, addEventListener() {}, removeEventListener() {} }));
    render(AgentSettings, { props: props(imgAgent) });
    const tiles = [...screen.getByTestId("avatar-poses").querySelectorAll("[data-testid=image-avatar]")];
    expect(tiles.length).toBe(13);
    for (const t of tiles) expect(t.classList.contains("animate")).toBe(false);
    expect(screen.getByTestId("avatar-pose-wait").querySelector("[data-fx=wave]")).not.toBeNull();
    expect(screen.getByTestId("avatar-pose-send").querySelector("[data-fx=send]")).not.toBeNull();
    vi.unstubAllGlobals();
  });

  test("Classic and Blob have no Poses grid", () => {
    const { unmount } = render(AgentSettings, { props: props(agent) });
    expect(screen.queryByTestId("avatar-poses")).toBeNull();
    unmount();
    render(AgentSettings, { props: props({ ...agent, avatar: { kind: "blob", shape: "cloud", color: "#8b5cf6" } } as unknown as AgentItem) });
    expect(screen.queryByTestId("avatar-poses")).toBeNull();
    vi.unstubAllGlobals();
  });
});
