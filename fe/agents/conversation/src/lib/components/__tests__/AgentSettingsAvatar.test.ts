import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const updateAgent = vi.fn((_b: string, _id: string, body: unknown) => Promise.resolve({ ...agent, ...(body as object) }));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  updateAgent: (b: string, id: string, body: unknown) => updateAgent(b, id, body),
  listAgentConnectors: () => Promise.resolve([]),
  getProjectPersona: () => Promise.resolve(null),
  runApi: <T,>(p: Promise<T>) => p,
}));
vi.mock("../../api/options.js", () => ({
  getProviderOptions: () => Promise.resolve([]),
  getProjectOptions: () => Promise.resolve([]),
}));

import AgentSettings from "../AgentSettings.svelte";
import type { AgentItem } from "../../api/team.js";

const agent = {
  id: "a1", handle: "worker", is_captain: false, project_id: "p1", name: "Worker", icon: "",
  description: "", system_prompt: "", provider: "", model: "", preset: "",
  features: {}, avatar: { shape: "triangle", color: "#ef4444" }, allowed_connectors: [],
  include_new_connectors: false, disabled: false, main_session_id: "", last_active: null,
  last_preview: "", status: "idle",
} as unknown as AgentItem;

const lastAvatar = () => (updateAgent.mock.calls.at(-1)?.[2] as { avatar?: unknown } | undefined)?.avatar;

describe("AgentSettings avatar: blob kind", () => {
  beforeEach(() => {
    updateAgent.mockClear();
    // jsdom has no canvas: blob previews fall back to a dot.
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
  });

  test("Blob swaps in the blob picker and saves kind + expression", async () => {
    render(AgentSettings, {
      props: { base: "/tools/agents", agent, agents: [agent], tab: "avatar", onTab: vi.fn(), onClose: vi.fn(), onSaved: vi.fn(), onDeleted: vi.fn() },
    });
    expect(screen.queryByTestId("blob-picker")).toBeNull();
    await fireEvent.click(screen.getByTestId("avatar-kind-blob"));
    expect(screen.getByTestId("blob-picker")).toBeDefined();
    await fireEvent.click(screen.getByRole("button", { name: "cloud" }));
    await fireEvent.click(screen.getByRole("button", { name: "happy" }));
    // Settings autosave: the last write carries the whole blob look.
    await waitFor(() => expect(lastAvatar()).toEqual({ kind: "blob", shape: "cloud", color: "#ef4444", expression: "happy" }));
  });

  test("back to Classic sends no blob keys", async () => {
    const blobAgent = { ...agent, avatar: { kind: "blob", shape: "cloud", color: "#111111", expression: "sad" } } as unknown as AgentItem;
    render(AgentSettings, {
      props: { base: "/tools/agents", agent: blobAgent, agents: [blobAgent], tab: "avatar", onTab: vi.fn(), onClose: vi.fn(), onSaved: vi.fn(), onDeleted: vi.fn() },
    });
    expect(screen.getByTestId("blob-picker")).toBeDefined();
    await fireEvent.click(screen.getByTestId("avatar-kind-classic"));
    expect(screen.queryByTestId("blob-picker")).toBeNull();
    await waitFor(() => expect(lastAvatar()).toEqual({ shape: "circle", color: "#111111" }));
  });
});
