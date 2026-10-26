import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const updateAgent = vi.fn((_b: string, _id: string, body: unknown) => Promise.resolve({ ...own, ...(body as object) }));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  updateAgent: (b: string, id: string, body: unknown) => updateAgent(b, id, body),
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
  id: "a1", handle: "worker", name: "Worker", is_captain: false,
};
const own = { ...base, own_project: true, show_in_projects: false } as unknown as AgentItem;
const props = (agent: AgentItem) => ({
  base: "/tools/agents", agent, agents: [agent], tab: "advanced", onTab: vi.fn(), onClose: vi.fn(), onSaved: vi.fn(), onDeleted: vi.fn(),
});
const lastBody = () => updateAgent.mock.calls.at(-1)?.[2] as Record<string, unknown> | undefined;

describe("AgentSettings › Advanced › Provider in chats", () => {
  beforeEach(() => updateAgent.mockClear());

  test("one choice replaces the two switches; Locked autosaves both fields", async () => {
    render(AgentSettings, { props: props({ ...own, allow_provider_switch: true } as unknown as AgentItem) });
    expect(screen.queryByRole("switch", { name: "Allow provider switch in chat" })).toBeNull();
    expect(screen.queryByRole("switch", { name: "Keep every chat on the default provider" })).toBeNull();
    const pick = screen.getByRole("radio", { name: /Chat may pick/ }) as HTMLInputElement;
    expect(pick.checked).toBe(true);
    expect(screen.getByTestId("as-provider-lock").textContent).toContain("A chat running on a failover target stays there");
    await fireEvent.click(screen.getByRole("radio", { name: /Locked to default/ }));
    await waitFor(() => expect(lastBody()).toMatchObject({ allow_provider_switch: false, force_default_provider: true }));
  });

  test("an agent that may not switch reads as Locked", () => {
    render(AgentSettings, { props: props({ ...own, allow_provider_switch: false, force_default_provider: true } as unknown as AgentItem) });
    expect((screen.getByRole("radio", { name: /Locked to default/ }) as HTMLInputElement).checked).toBe(true);
  });
});
