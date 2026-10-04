import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const updateAgent = vi.fn((_b: string, _id: string, body: unknown) => Promise.resolve({ ...worker, ...(body as object) }));
const getAccessHistory = vi.fn(() => Promise.resolve({ items: [
  { id: "h1", actor: "@captain", status: "applied", diff: ["+Notion (read)"], decided_by: "Yoga", at: "2026-10-03T10:00:00Z" },
  { id: "h2", actor: "@captain", status: "declined", diff: ["-Slack"], decided_by: "Yoga", at: "2026-10-03T09:00:00Z" },
] }));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  updateAgent: (b: string, id: string, body: unknown) => updateAgent(b, id, body),
  getAccessHistory: () => getAccessHistory(),
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

const base = {
  project_id: "p1", icon: "", description: "", system_prompt: "", provider: "", model: "", preset: "",
  features: {}, avatar: { shape: "circle", color: "#6366f1" }, allowed_connectors: [],
  include_new_connectors: false, disabled: false, main_session_id: "", last_active: null, last_preview: "", status: "idle",
};
const worker = { ...base, id: "a1", handle: "worker", name: "Worker", is_captain: false } as unknown as AgentItem;
const captain = { ...base, id: "c1", handle: "captain", name: "Captain", is_captain: true, manage_agents: true } as unknown as AgentItem;
const props = (agent: AgentItem, tab: string) => ({
  base: "/tools/agents", agent, agents: [captain, worker], tab, onTab: vi.fn(), onClose: vi.fn(), onSaved: vi.fn(), onDeleted: vi.fn(),
});
const lastBody = () => updateAgent.mock.calls.at(-1)?.[2] as Record<string, unknown> | undefined;

describe("AgentSettings › Captain", () => {
  beforeEach(() => { updateAgent.mockClear(); getAccessHistory.mockClear(); });

  test("a teammate shows the three Captain-can toggles with defaults", () => {
    render(AgentSettings, { props: props(worker, "captain") });
    expect(screen.getByTestId("captain-can")).toBeDefined();
    expect(screen.getByRole("switch", { name: "Edit persona" }).getAttribute("aria-checked")).toBe("true");
    expect(screen.getByRole("switch", { name: "Propose access changes" }).getAttribute("aria-checked")).toBe("false");
    expect(screen.getByRole("switch", { name: "Manage routines" }).getAttribute("aria-checked")).toBe("true");
    expect(screen.getByTestId("captain-can").textContent).toContain("accept or decline on a card");
    expect(screen.queryByTestId("captain-manage")).toBeNull();
  });

  test("toggling autosaves captain_can", async () => {
    render(AgentSettings, { props: props(worker, "captain") });
    await fireEvent.click(screen.getByRole("switch", { name: "Propose access changes" }));
    await waitFor(() => expect(lastBody()?.captain_can).toEqual({ persona: true, access: true, routines: true }));
  });

  test("the Captain gets Manage other agents instead", async () => {
    render(AgentSettings, { props: props(captain, "captain") });
    expect(screen.queryByTestId("captain-can")).toBeNull();
    const sw = screen.getByRole("switch", { name: "Manage other agents" });
    expect(sw.getAttribute("aria-checked")).toBe("true");
    await fireEvent.click(sw);
    await waitFor(() => expect(lastBody()?.manage_agents).toBe(false));
  });

  test("Access shows a folded History that loads when opened", async () => {
    render(AgentSettings, { props: props(worker, "access") });
    const h = screen.getByTestId("access-history") as HTMLDetailsElement;
    expect(h.open).toBe(false);
    expect(getAccessHistory).not.toHaveBeenCalled();
    h.open = true;
    await fireEvent(h, new Event("toggle"));
    await waitFor(() => expect(screen.getAllByTestId("access-history-row")).toHaveLength(2));
    expect(h.textContent).toContain("+Notion (read)");
    expect(h.textContent).toContain("approved by Yoga");
    expect(h.textContent).toContain("declined by Yoga");
  });
});
