import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const updateAgent = vi.fn((_b: string, _id: string, body: unknown) => Promise.resolve({ ...agent, ...(body as object) }));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  updateAgent: (b: string, id: string, body: unknown) => updateAgent(b, id, body),
  getAccessHistory: () => Promise.resolve({ items: [] }),
  listAgentConnectors: () => Promise.resolve([]),
  getProjectPersona: () => Promise.resolve(null),
  runApi: <T,>(p: Promise<T>) => p,
}));
vi.mock("../../api/options.js", () => ({
  getProviderOptions: () => Promise.resolve([]),
  getProjectOptions: () => Promise.resolve([]),
}));

import AgentSettings from "../AgentSettings.svelte";
import { promptsToSave } from "../../suggestedPrompts.js";
import type { AgentItem } from "../../api/team.js";

const agent = {
  id: "a1", handle: "worker", name: "Worker", is_captain: false,
  project_id: "p1", icon: "", description: "", system_prompt: "", provider: "claude", model: "", preset: "",
  features: {}, avatar: { shape: "circle", color: "#6366f1" }, allowed_connectors: [],
  include_new_connectors: false, disabled: false, main_session_id: "s1", last_active: null, last_preview: "", status: "idle",
  suggested_prompts: [{ title: "Recap", message: "Recap today" }, { title: "a", message: "a" }, { title: "b", message: "b" }],
} as unknown as AgentItem;

describe("AgentSettings › Persona › Suggested prompts", () => {
  test("edits autosave and the add button stops at four", async () => {
    render(AgentSettings, { props: { base: "/tools/agents", agent, agents: [agent], tab: "persona", onTab: vi.fn(), onClose: vi.fn(), onSaved: vi.fn(), onDeleted: vi.fn() } });
    expect((screen.getByLabelText("Prompt 1 title") as HTMLInputElement).value).toBe("Recap");
    await fireEvent.click(screen.getByText("+ Add prompt"));
    expect(screen.queryByText("+ Add prompt")).toBeNull();
    await fireEvent.input(screen.getByLabelText("Prompt 4 title"), { target: { value: "Logs" } });
    await waitFor(() => {
      const body = updateAgent.mock.calls.at(-1)?.[2] as { suggested_prompts?: unknown[] } | undefined;
      expect(body?.suggested_prompts).toHaveLength(4);
    }, { timeout: 3000 });
  });

  test("promptsToSave mirrors the server's normalisation", () => {
    expect(promptsToSave([{ title: " x ", message: "" }, { title: "", message: "" }, { title: "", message: "y" }])).toEqual([
      { title: "x", message: "x" }, { title: "y", message: "y" },
    ]);
  });
});
