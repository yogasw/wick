import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const updateAgent = vi.fn((_b: string, _id: string, body: unknown) => Promise.resolve({ ...agent, ...(body as object) }));
const sendToChat = vi.fn((_b: string, _s: string, _t: string) => Promise.resolve({ status: "sent" }));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  updateAgent: (b: string, id: string, body: unknown) => updateAgent(b, id, body),
  getAccessHistory: () => Promise.resolve({ items: [] }),
  listAgentConnectors: () => Promise.resolve([]),
  getProjectPersona: () => Promise.resolve(null),
  getAgentSkills: () => Promise.resolve({ local_dir: "/p/files/.claude/skills", items: [
    { name: "deploy", description: "ship it", source: "local", disabled: false },
    { name: "loki", description: "logs", source: "global", disabled: true },
    { name: "wick-agent-cards", description: "cards", source: "builtin", required: true, disabled: false },
    { name: "wick-notes", description: "mine", source: "local", overrides: "builtin", disabled: false },
    { name: "wick-notes", description: "shipped", source: "builtin", shadowed: true, disabled: false },
  ] }),
  sendToChat: (b: string, s: string, t: string) => sendToChat(b, s, t),
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
  allowed_native_tools: ["Read", "Grep", "Glob", "WebFetch", "WebSearch"], bash_rules: [], disabled_skills: ["loki"],
  native_tools_enforced: true,
} as unknown as AgentItem;
const props = (a: AgentItem, tab: string) => ({
  base: "/tools/agents", agent: a, agents: [a], tab, onTab: vi.fn(), onClose: vi.fn(), onSaved: vi.fn(), onDeleted: vi.fn(),
});
const lastBody = () => updateAgent.mock.calls.at(-1)?.[2] as Record<string, unknown> | undefined;

describe("AgentSettings › Tools & features", () => {
  beforeEach(() => updateAgent.mockClear());

  test("native tools start with Bash/Edit/Write off and toggle autosaves", async () => {
    render(AgentSettings, { props: props(agent, "tools") });
    expect(screen.getByRole("switch", { name: "Read" }).getAttribute("aria-checked")).toBe("true");
    expect(screen.getByRole("switch", { name: "Bash" }).getAttribute("aria-checked")).toBe("false");
    expect(screen.queryByTestId("tools-not-enforced")).toBeNull();
    await fireEvent.click(screen.getByRole("switch", { name: "Bash" }));
    await waitFor(() => expect(lastBody()?.allowed_native_tools).toEqual(["Read", "Grep", "Glob", "WebFetch", "WebSearch", "Bash"]));
  });

  test("a Bash rule with chaining is refused inline, a clean one saves", async () => {
    render(AgentSettings, { props: props({ ...agent, allowed_native_tools: ["Read", "Bash"] } as AgentItem, "tools") });
    expect(screen.getByTestId("bash-rules").textContent).toContain("every command asks you first");
    const input = screen.getByLabelText("Command pattern");
    await fireEvent.input(input, { target: { value: "ls | sh" } });
    await fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(screen.getByTestId("bash-pattern-error").textContent).toContain("One command per rule");
    await fireEvent.input(input, { target: { value: "git status" } });
    await fireEvent.click(screen.getByRole("button", { name: "Add" }));
    await waitFor(() => expect(lastBody()?.bash_rules).toEqual([{ pattern: "git status", scope: "{project}" }]));
  });

  test("a provider that is not held to the switches says so", () => {
    render(AgentSettings, { props: props({ ...agent, provider: "codex", native_tools_enforced: false } as AgentItem, "tools") });
    expect(screen.getByTestId("tools-not-enforced").textContent).toContain("Not enforced on codex");
  });
});

describe("AgentSettings › Skills", () => {
  beforeEach(() => { updateAgent.mockClear(); sendToChat.mockClear(); });

  test("lists sources, local wins, required, and toggles disabled_skills", async () => {
    render(AgentSettings, { props: props(agent, "skills") });
    const list = await screen.findByTestId("skills-list");
    expect(list.textContent).toContain("Local");
    expect(list.textContent).toContain("Built-in");
    expect(list.textContent).toContain("a local skill with the same name wins");
    expect(list.textContent).toContain("required");
    expect(screen.queryByRole("switch", { name: "wick-agent-cards" })).toBeNull();
    expect(screen.getByRole("switch", { name: "loki" }).getAttribute("aria-checked")).toBe("false");
    await fireEvent.click(screen.getByRole("switch", { name: "deploy" }));
    await waitFor(() => expect(lastBody()?.disabled_skills).toEqual(["deploy", "loki"]));
  });

  test("Create skill from this chat sends an instruction to the agent's chat", async () => {
    render(AgentSettings, { props: props(agent, "skills") });
    await screen.findByTestId("skills-list");
    await fireEvent.click(screen.getByTestId("create-skill"));
    await waitFor(() => expect(sendToChat).toHaveBeenCalled());
    expect(sendToChat.mock.calls[0][1]).toBe("s1");
    expect(sendToChat.mock.calls[0][2]).toContain("/p/files/.claude/skills/<kebab-name>/SKILL.md");
  });
});
