import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/svelte";

/* The roster carries the stored switches only; opening Settings loads the
   agent from GET /api/team/agents/{id} (catalog-resolved, access migrated),
   shows a skeleton meanwhile, and hands the result to the roster. */
const updateAgent = vi.fn((_b: string, _id: string, body: unknown) => Promise.resolve({ ...agent, ...(body as object) }));
let release: (a: unknown) => void = () => {};
const getAgent = vi.fn((_b: string, _id: string) => new Promise((r) => { release = r; }));
const catalog = [
  { id: "c1", key: "slack", label: "Slack", description: "", accounts: [], ops: [{ key: "send", name: "Send", destructive: false }], tier: "" },
  { id: "n1", key: "notes", label: "Notes", description: "", accounts: [], ops: [{ key: "list", name: "List", destructive: false }], tier: "" },
];
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  updateAgent: (b: string, id: string, body: unknown) => updateAgent(b, id, body),
  getAgent: (b: string, id: string) => getAgent(b, id),
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
  id: "a1", handle: "worker", name: "Worker", is_captain: false,
  project_id: "p1", icon: "", description: "", system_prompt: "", provider: "claude", model: "", preset: "",
  features: { notes: false }, avatar: { shape: "circle", color: "#6366f1" },
  allowed_connectors: [{ connector_id: "c1", accounts: [], level: "read", ops: [] }],
  include_new_connectors: false, run_as: "caller", disabled: false, main_session_id: "s1", last_active: null, last_preview: "", status: "idle",
} as unknown as AgentItem;
// The server's copy: the old Notes switch migrated into an off grant.
const resolved = {
  ...agent,
  features: { notes: false, browser: false },
  allowed_connectors: [
    { connector_id: "c1", accounts: [], level: "read", ops: [] },
    { connector_id: "n1", accounts: [], level: "off", ops: [] },
  ],
} as unknown as AgentItem;

describe("AgentSettings loads the agent when opened", () => {
  beforeEach(() => { updateAgent.mockClear(); getAgent.mockClear(); });

  test("skeleton until GET /agents/{id} answers, then its copy, with no PATCH", async () => {
    const onSaved = vi.fn();
    render(AgentSettings, { props: { base: "/tools/agents", agent, agents: [agent], tab: "access", onTab: vi.fn(), onClose: vi.fn(), onSaved, onDeleted: vi.fn() } });
    expect(getAgent).toHaveBeenCalledWith("/tools/agents", "a1");
    expect(screen.getByTestId("access-skeleton")).toBeDefined();
    expect(screen.queryByLabelText("Select Slack")).toBeNull();

    release(resolved);
    await screen.findByLabelText("Select Slack");
    expect(screen.queryByTestId("access-skeleton")).toBeNull();
    expect(onSaved).toHaveBeenCalledWith(resolved);
    await new Promise((r) => setTimeout(r, 1200));
    expect(updateAgent).not.toHaveBeenCalled();
  });

  test("a failed load still opens the roster copy", async () => {
    getAgent.mockImplementationOnce(() => Promise.reject(new Error("boom")));
    render(AgentSettings, { props: { base: "/tools/agents", agent, agents: [agent], tab: "access", onTab: vi.fn(), onClose: vi.fn(), onSaved: vi.fn(), onDeleted: vi.fn() } });
    await screen.findByLabelText("Select Slack");
    await waitFor(() => expect(screen.queryByTestId("access-skeleton")).toBeNull());
  });
});
