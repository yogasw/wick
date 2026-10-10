import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const toastError = vi.fn();
vi.mock("@wick-fe/common-stores", async (orig) => ({
  ...(await orig<typeof import("@wick-fe/common-stores")>()),
  toastError: (t: string, b?: string) => toastError(t, b),
}));
const uploadAvatarImage = vi.fn((_b: string, _id: string, _d: string) => Promise.reject(new Error("only PNG, JPEG or WebP images are accepted")));
const updateAgent = vi.fn((_b: string, id: string, body: unknown) => Promise.resolve({ id, handle: "pic", ...(body as object) }));
vi.mock("../../avatarImage.js", async (orig) => ({
  ...(await orig<typeof import("../../avatarImage.js")>()),
  fileData: () => Promise.resolve("iVBORw=="),
}));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  createAgent: () => Promise.resolve({ id: "a1", handle: "pic", avatar: { shape: "circle", color: "#27b199" } }),
  uploadAvatarImage: (b: string, id: string, d: string) => uploadAvatarImage(b, id, d),
  updateAgent: (b: string, id: string, body: unknown) => updateAgent(b, id, body),
  listAgentConnectors: () => Promise.resolve([]),
  listAgents: () => Promise.resolve({ agents: [], captain_id: "" }),
  getProjectPersona: () => Promise.resolve({ name: "", description: "", system_prompt: "" }),
  runApi: <T,>(p: Promise<T>) => p,
}));
vi.mock("../../api/options.js", () => ({
  getProjectOptions: () => Promise.resolve([]),
  getProviderOptions: () => Promise.resolve([{ type: "claude", name: "claude", models: [] }]),
  getProviderOptionModels: () => Promise.resolve([{ id: "sonnet", label: "Sonnet", default: true }]),
}));
vi.stubGlobal("fetch", vi.fn(() => Promise.resolve({ ok: false, json: () => Promise.resolve({}) })));

import AgentWizard from "../AgentWizard.svelte";

const props = () => ({ base: "/tools/agents", taken: [] as string[], onClose: vi.fn(), onCreated: vi.fn() });

describe("AgentWizard › avatar style Image", () => {
  test("the create flow offers Classic, Blob and Image", () => {
    render(AgentWizard, { props: props() });
    const group = screen.getByRole("group", { name: "Avatar kind" });
    expect([...group.querySelectorAll("button")].map((b) => b.textContent?.trim())).toEqual(["Classic", "Blob", "Image"]);
  });

  test("Image shows the crop editor; a picked file previews from a blob: URL; Reset clears it", async () => {
    const url = `blob:${location.origin}/0000-pic`;
    URL.createObjectURL = vi.fn(() => url);
    URL.revokeObjectURL = vi.fn();
    render(AgentWizard, { props: props() });
    await fireEvent.click(screen.getByTestId("avatar-kind-image"));
    expect(screen.getByTestId("avatar-image-editor")).toBeDefined();
    expect(screen.getByText("No image yet")).toBeDefined();
    const file = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], "pic.png", { type: "image/png" });
    await fireEvent.change(screen.getByTestId("avatar-image-file"), { target: { files: [file] } });
    expect(screen.getByTestId("avatar-image-frame").querySelector("img")?.getAttribute("src")).toBe(url);
    await fireEvent.input(screen.getByTestId("avatar-image-zoom"), { target: { value: "0.01" } });
    expect((screen.getByTestId("avatar-image-zoom") as HTMLInputElement).value).toBe("0.3");
    await fireEvent.click(screen.getByTestId("avatar-image-reset"));
    expect(screen.getByText("No image yet")).toBeDefined();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith(url);
  });

  test("the crop tiles wait for a picture; another picture keeps the shape; submit saves it (review M2/L1)", async () => {
    URL.createObjectURL = vi.fn(() => `blob:${location.origin}/0000-pic`);
    URL.revokeObjectURL = vi.fn();
    uploadAvatarImage.mockImplementationOnce(() => Promise.resolve({ v: "0123456789abcdef", aspect: 1 }) as never);
    const onCreated = vi.fn();
    render(AgentWizard, { props: { ...props(), onCreated } });
    await fireEvent.input(screen.getByLabelText("Name"), { target: { value: "Pic" } });
    await fireEvent.click(screen.getByTestId("avatar-kind-image"));
    expect((screen.getByRole("button", { name: "Hexagon" }) as HTMLButtonElement).disabled).toBe(true);
    const file = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], "pic.png", { type: "image/png" });
    await fireEvent.change(screen.getByTestId("avatar-image-file"), { target: { files: [file] } });
    await fireEvent.click(screen.getByRole("button", { name: "Hexagon" }));
    await fireEvent.change(screen.getByTestId("avatar-image-file"), { target: { files: [file] } });
    expect(screen.getByRole("button", { name: "Hexagon" }).getAttribute("aria-pressed")).toBe("true");
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    await fireEvent.click(screen.getByRole("button", { name: "Provider & model" }));
    await fireEvent.click(screen.getByRole("button", { name: /^Claude/ }));
    await fireEvent.click(await screen.findByText("Sonnet"));
    await fireEvent.click(screen.getByRole("button", { name: "Create agent" }));
    await waitFor(() => expect(onCreated).toHaveBeenCalled());
    expect(updateAgent.mock.calls.at(-1)?.[2]).toMatchObject({ avatar: { kind: "image", image: { v: "0123456789abcdef", crop: "hex", x: 0, zoom: 0 } } });
  });

  test("a GIF is refused with a message", async () => {
    render(AgentWizard, { props: props() });
    await fireEvent.click(screen.getByTestId("avatar-kind-image"));
    await fireEvent.change(screen.getByTestId("avatar-image-file"), { target: { files: [new File(["GIF89a"], "a.gif", { type: "image/gif" })] } });
    expect(screen.getByTestId("avatar-image-msg").textContent).toContain("PNG, JPEG or WebP");
  });

  test("M4: a failed upload after create says so, and the agent is still created", async () => {
    URL.createObjectURL = vi.fn(() => `blob:${location.origin}/0000-pic`);
    URL.revokeObjectURL = vi.fn();
    const onCreated = vi.fn();
    render(AgentWizard, { props: { ...props(), onCreated } });
    await fireEvent.input(screen.getByLabelText("Name"), { target: { value: "Pic" } });
    await fireEvent.click(screen.getByTestId("avatar-kind-image"));
    const file = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], "pic.png", { type: "image/png" });
    await fireEvent.change(screen.getByTestId("avatar-image-file"), { target: { files: [file] } });
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    await fireEvent.click(screen.getByRole("button", { name: "Provider & model" }));
    await fireEvent.click(screen.getByRole("button", { name: /^Claude/ }));
    await fireEvent.click(await screen.findByText("Sonnet"));
    await fireEvent.click(screen.getByRole("button", { name: "Create agent" }));
    await waitFor(() => expect(onCreated).toHaveBeenCalled());
    expect(onCreated.mock.calls[0][0]).toMatchObject({ id: "a1" });
    expect(uploadAvatarImage).toHaveBeenCalled();
    expect(toastError).toHaveBeenCalledWith("Agent created, but the image was not saved", expect.stringContaining("Settings › Avatar"));
  });
});
