import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const updateAgent = vi.fn((_b: string, _id: string, body: unknown) => Promise.resolve({ ...worker, ...(body as object) }));
const getAccessHistory = vi.fn(() => Promise.resolve({ items: [
  { id: "h1", actor: "@captain", status: "applied", diff: ["+Notion (read)"], decided_by: "Yoga", at: "2026-10-03T10:00:00Z" },
  { id: "h2", actor: "@captain", status: "declined", diff: ["-Slack"], decided_by: "Yoga", at: "2026-10-03T09:00:00Z" },
] }));
let shareCount = 0;
const makeCaptain = vi.fn((_b: string, id: string) => Promise.resolve({ agent: { ...worker, id, is_captain: true, manage_agents: true }, previous_id: "c1" }));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  updateAgent: (b: string, id: string, body: unknown) => updateAgent(b, id, body),
  getAccessHistory: () => getAccessHistory(),
  listAgentConnectors: () => Promise.resolve([]),
  getAgent: () => Promise.resolve(null),
  getProjectPersona: () => Promise.resolve(null),
  listAgentShares: () => Promise.resolve({ shares: Array.from({ length: shareCount }, (_, i) => ({ user_id: `u${i}`, name: "x", created_at: "" })), shareable: true, reason: "" }),
  makeCaptain: (b: string, id: string) => makeCaptain(b, id),
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
  beforeEach(() => { updateAgent.mockClear(); getAccessHistory.mockClear(); makeCaptain.mockClear(); shareCount = 0; });

  test("a teammate gets Make Captain, confirmed in a dialog naming the current Captain", async () => {
    const p = props(worker, "captain");
    render(AgentSettings, { props: p });
    const btn = screen.getByRole("button", { name: "Make Captain" });
    await waitFor(() => expect(btn.hasAttribute("disabled")).toBe(false));
    expect(screen.getByTestId("captain-make").textContent).toContain("@captain is your Captain now");
    await fireEvent.click(btn);
    const dlg = await screen.findByTestId("captain-confirm");
    expect(dlg.textContent).toContain("@captain stops being the Captain");
    expect(dlg.textContent).toContain("@worker takes over coordinating your Team");
    expect(makeCaptain).not.toHaveBeenCalled();
    const confirm = screen.getAllByRole("button", { name: "Make Captain" }).at(-1)!;
    await fireEvent.click(confirm);
    await waitFor(() => expect(makeCaptain).toHaveBeenCalledWith("/tools/agents", "a1"));
    await waitFor(() => expect(p.onSaved).toHaveBeenCalledWith(expect.objectContaining({ id: "a1", is_captain: true })));
    // The transfer is its own call, never a PATCH of is_captain.
    expect(updateAgent.mock.calls.some((c) => "is_captain" in (c[2] as object))).toBe(false);
  });

  test("the Captain shows a read-only status, no Make Captain and no switch", () => {
    render(AgentSettings, { props: props(captain, "captain") });
    expect(screen.getByTestId("captain-status").textContent).toContain("This agent is your Team's Captain.");
    expect(screen.getByTestId("captain-status").textContent).toContain("Make Captain");
    expect(screen.queryByRole("button", { name: "Make Captain" })).toBeNull();
    expect(screen.queryByRole("switch", { name: /captain/i })).toBeNull();
  });

  test("a shared agent cannot be made Captain and says why", async () => {
    shareCount = 1;
    render(AgentSettings, { props: props(worker, "captain") });
    await waitFor(() => expect(screen.getByTestId("captain-make").textContent).toContain("stop sharing it first"));
    expect(screen.getByRole("button", { name: "Make Captain" }).hasAttribute("disabled")).toBe(true);
  });

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
