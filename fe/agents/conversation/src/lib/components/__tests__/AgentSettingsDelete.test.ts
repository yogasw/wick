import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const deleteAgent = vi.fn((..._a: unknown[]) => Promise.resolve({}));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  deleteAgent: (...a: unknown[]) => deleteAgent(...a),
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
  features: {}, avatar: { shape: "circle", color: "#888" }, allowed_connectors: [],
  include_new_connectors: false, disabled: false, main_session_id: "", last_active: null,
  last_preview: "", status: "idle",
} as unknown as AgentItem;

function mount(onDeleted = vi.fn()) {
  render(AgentSettings, {
    props: { base: "/tools/agents", agent, agents: [agent], tab: "advanced", onTab: vi.fn(), onClose: vi.fn(), onSaved: vi.fn(), onDeleted },
  });
  return onDeleted;
}

describe("AgentSettings delete agent", () => {
  beforeEach(() => deleteAgent.mockClear());

  test("defaults to deleting chats and memory too", async () => {
    const onDeleted = mount();
    await fireEvent.click(screen.getByRole("button", { name: "Delete agent…" }));
    expect(screen.getByText("Delete agent Worker?")).toBeDefined();
    await fireEvent.click(screen.getByRole("button", { name: "Delete agent and chats" }));
    await waitFor(() => expect(onDeleted).toHaveBeenCalled());
    expect(deleteAgent).toHaveBeenCalledWith("/tools/agents", "a1", "delete");
  });

  test("keep mode leaves the chats as a normal project", async () => {
    mount();
    await fireEvent.click(screen.getByRole("button", { name: "Delete agent…" }));
    await fireEvent.click(screen.getByLabelText(/Keep its chats as a normal project/));
    await fireEvent.click(screen.getByRole("button", { name: "Delete agent" }));
    await waitFor(() => expect(deleteAgent).toHaveBeenCalledWith("/tools/agents", "a1", "keep"));
  });
});
