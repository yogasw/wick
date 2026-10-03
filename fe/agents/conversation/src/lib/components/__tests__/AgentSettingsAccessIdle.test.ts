import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

/* Opening Settings › Access must not save anything by itself: the autosave
   only fires for a real change, once, and its own response does not count
   as another change. */
const updateAgent = vi.fn((_b: string, _id: string, body: unknown) => Promise.resolve({ ...agent, ...(body as object) }));
const catalog = [
  { id: "c1", key: "slack", label: "Slack", description: "", accounts: [], ops: [{ key: "send", name: "Send", destructive: false }], tier: "" },
  { id: "c2", key: "loki", label: "Loki", description: "", accounts: [], ops: [{ key: "query", name: "Query", destructive: false }], tier: "" },
];
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  updateAgent: (b: string, id: string, body: unknown) => updateAgent(b, id, body),
  getAccessHistory: () => Promise.resolve({ items: [] }),
  listAgentConnectors: () => Promise.resolve(catalog),
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
  id: "a1", handle: "captain", name: "Captain", is_captain: true,
  project_id: "p1", icon: "", description: "", system_prompt: "", provider: "claude", model: "", preset: "",
  features: {}, avatar: { shape: "circle", color: "#6366f1" },
  allowed_connectors: [{ connector_id: "c1", accounts: [], level: "read", ops: [] }],
  include_new_connectors: false, run_as: "caller", disabled: false, main_session_id: "s1", last_active: null, last_preview: "", status: "idle",
} as unknown as AgentItem;
const props = (a: AgentItem) => ({
  base: "/tools/agents", agent: a, agents: [a], tab: "access", onTab: vi.fn(), onClose: vi.fn(), onSaved: vi.fn(), onDeleted: vi.fn(),
});
const settle = () => new Promise((r) => setTimeout(r, 1200));

describe("AgentSettings › Access autosave", () => {
  beforeEach(() => updateAgent.mockClear());

  test("opening the tab and leaving it alone sends no PATCH", async () => {
    render(AgentSettings, { props: props(agent) });
    await screen.findByLabelText("Grant Slack");
    await settle();
    expect(updateAgent).not.toHaveBeenCalled();
  });

  test("one change sends exactly one PATCH, and its response triggers no other", async () => {
    render(AgentSettings, { props: props(agent) });
    await screen.findByLabelText("Grant Slack");
    await fireEvent.input(screen.getByLabelText("Search Connectors"), { target: { value: "Loki" } });
    await fireEvent.click(await screen.findByRole("button", { name: "Add Loki read only" }));
    await waitFor(() => expect(updateAgent).toHaveBeenCalledTimes(1));
    expect(Object.keys(updateAgent.mock.calls[0][2] as object)).toEqual(["allowed_connectors"]);
    await settle();
    expect(updateAgent).toHaveBeenCalledTimes(1);
  });
});
