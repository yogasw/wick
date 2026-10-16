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
const SW = "Show teammates in channels";

describe("AgentSettings › Advanced › Show teammates in channels", () => {
  beforeEach(() => updateAgent.mockClear());

  test("on by default, with its help text, and autosaves off", async () => {
    render(AgentSettings, { props: props(own) });
    const sw = screen.getByRole("switch", { name: SW });
    expect(sw.getAttribute("aria-checked")).toBe("true");
    expect(screen.getByTestId("as-show-teammates").textContent).toContain("When off, channels see one agent working; teammates and their names stay in the web view only.");
    await fireEvent.click(sw);
    await waitFor(() => expect(lastBody()?.channel_show_teammates).toBe(false));
  });

  test("reads a stored off", () => {
    render(AgentSettings, { props: props({ ...own, channel_show_teammates: false } as unknown as AgentItem) });
    expect(screen.getByRole("switch", { name: SW }).getAttribute("aria-checked")).toBe("false");
  });
});
