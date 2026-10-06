import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const deleteAgent = vi.fn((..._a: unknown[]) => Promise.resolve({}));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  deleteAgent: (...a: unknown[]) => deleteAgent(...a),
  listAgentConnectors: () => Promise.resolve([]),
  getAgent: () => Promise.resolve(null),
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
  beforeEach(() => {
    deleteAgent.mockClear();
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response(JSON.stringify({ chats: 4, channels: ["slack · C01"], schedules: 1 })))));
  });

  test("defaults to deleting the project too, behind a red alert and the typed name", async () => {
    const onDeleted = mount();
    await fireEvent.click(screen.getByRole("button", { name: "Delete agent…" }));
    expect(screen.getByText("Delete agent Worker?")).toBeDefined();
    expect((screen.getByLabelText(/Also delete its project/) as HTMLInputElement).checked).toBe(true);
    await waitFor(() => expect(screen.getByTestId("agent-delete-alert").textContent).toContain("4 chats"));
    expect(screen.getByTestId("agent-delete-alert").textContent).toContain("slack · C01");
    const btn = screen.getByRole("button", { name: "Delete agent and project" }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    await fireEvent.input(screen.getByLabelText("Agent name"), { target: { value: "Worker" } });
    expect(btn.disabled).toBe(false);
    await fireEvent.click(btn);
    await waitFor(() => expect(onDeleted).toHaveBeenCalled());
    expect(deleteAgent).toHaveBeenCalledWith("/tools/agents", "a1", "delete");
  });

  test("unticked keeps the chats as a normal project, no name needed", async () => {
    mount();
    await fireEvent.click(screen.getByRole("button", { name: "Delete agent…" }));
    await fireEvent.click(screen.getByLabelText(/Also delete its project/));
    expect(screen.getByText("Its chats stay as a normal project in the sidebar.")).toBeDefined();
    expect(screen.queryByTestId("agent-delete-alert")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Delete agent" }));
    await waitFor(() => expect(deleteAgent).toHaveBeenCalledWith("/tools/agents", "a1", "keep"));
  });

  test("Danger zone labels both controls: Disable is a checkbox with what it does", async () => {
    mount();
    const box = screen.getByRole("checkbox", { name: /^Disable agent/ }) as HTMLInputElement;
    expect(box.checked).toBe(false);
    expect(screen.getByTestId("danger-zone").textContent).toContain("Its chats and memory stay.");
    expect(screen.getByText("Delete agent")).toBeDefined();
    await fireEvent.click(box);
    expect(box.checked).toBe(true);
  });
});
