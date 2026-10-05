import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const updateAgent = vi.fn((_b: string, _id: string, body: unknown) => Promise.resolve({ ...agent, ...(body as object) }));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  updateAgent: (b: string, id: string, body: unknown) => updateAgent(b, id, body),
  getAccessHistory: () => Promise.resolve({ items: [] }),
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
  id: "a1", handle: "worker", name: "Worker", is_captain: false,
  project_id: "p1", icon: "", description: "", system_prompt: "", provider: "claude", model: "", preset: "",
  features: {}, avatar: { shape: "circle", color: "#6366f1" }, allowed_connectors: [],
  include_new_connectors: false, disabled: false, main_session_id: "s1", last_active: null, last_preview: "", status: "idle",
  suggested_prompts: [{ title: "Recap", message: "Recap today" }, { title: "a", message: "a" }, { title: "b", message: "b" }],
} as unknown as AgentItem;

describe("AgentSettings › Persona › Improve with AI", () => {
  const props = { base: "/tools/agents", agent, agents: [agent], tab: "persona", onTab: vi.fn(), onClose: vi.fn(), onSaved: vi.fn(), onDeleted: vi.fn() };

  test("one panel with an instruction and the fields to update; no per-field buttons", () => {
    render(AgentSettings, { props });
    expect(screen.getByLabelText("✨ Improve with AI")).toBeTruthy();
    expect((screen.getByTestId("as-improve-name") as HTMLInputElement).checked).toBe(false);
    for (const k of ["tagline", "description", "system_prompt"]) {
      expect((screen.getByTestId(`as-improve-${k}`) as HTMLInputElement).checked).toBe(true);
    }
    expect(screen.queryByTestId("as-gen-desc")).toBeNull();
    expect(screen.queryByTestId("as-gen-sys")).toBeNull();
  });

  test("refuses with no field ticked", async () => {
    render(AgentSettings, { props });
    for (const k of ["tagline", "description", "system_prompt"]) await fireEvent.click(screen.getByTestId(`as-improve-${k}`));
    await fireEvent.input(screen.getByLabelText("✨ Improve with AI"), { target: { value: "lebih formal" } });
    await fireEvent.click(screen.getByTestId("as-gen-button"));
    expect((await screen.findByRole("alert")).textContent).toContain("Pick at least one field to update.");
  });
});
