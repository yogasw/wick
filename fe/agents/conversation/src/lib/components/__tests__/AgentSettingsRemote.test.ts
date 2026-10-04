import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { AgentItem, RemoteAgentInfo } from "../../api/team.js";

const info: RemoteAgentInfo = {
  card_url: "https://research.example.com/.well-known/agent-card.json",
  host: "research.example.com",
  card: {
    name: "Research Bot", description: "", version: "1.4.0", streaming: false,
    skills: [{ id: "s1", name: "Search papers" }], endpoint: "https://research.example.com/a2a", transport: "JSONRPC",
  },
  auth_type: "bearer", auth_set: true, timeout_sec: 120, max_response_bytes: 2097152, usage: "only_me",
  refreshed_at: "2026-10-03T10:00:00Z",
};
const get = vi.fn(() => Promise.resolve(info));
const update = vi.fn((_b: string, _id: string, body: Record<string, unknown>) => Promise.resolve({ ...info, ...body, auth_type: (body.auth as { type?: string } | undefined)?.type ?? info.auth_type }));
const refresh = vi.fn(() => Promise.resolve({ ...info, card: { ...info.card, version: "1.5.0", skills: [...(info.card.skills ?? []), { id: "s2", name: "Cite" }] } }));
const test_ = vi.fn(() => Promise.resolve({ ok: true, state: "message", card_ms: 3, latency_ms: 55, reply: "pong", error: "" }));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  getRemoteAgent: () => get(),
  updateRemoteAgent: (b: string, id: string, body: Record<string, unknown>) => update(b, id, body),
  refreshRemoteCard: () => refresh(),
  testRemoteAgent: () => test_(),
  updateAgent: (_b: string, _id: string, body: unknown) => Promise.resolve({ ...remote, ...(body as object) }),
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
import type { SettingsTab } from "../../agentsRouter.js";

const remote = {
  id: "r1", handle: "research-bot", name: "Research Bot", is_captain: false, kind: "a2a-remote", remote: info,
  project_id: "p1", icon: "", description: "", system_prompt: "", provider: "a2a-remote/a2a-remote", model: "", preset: "",
  features: {}, avatar: { shape: "circle", color: "#6366f1" }, allowed_connectors: [], allowed_native_tools: [],
  include_new_connectors: false, disabled: false, main_session_id: "", last_active: null, last_preview: "", status: "idle",
} as unknown as AgentItem;
// The panel re-reads the settings on open; edits start once that landed.
const loaded = async () => { await waitFor(() => expect(get).toHaveBeenCalled()); await new Promise((r) => setTimeout(r, 0)); };
const props = (tab: SettingsTab) => ({
  base: "/tools/agents", agent: remote, agents: [remote], tab, onTab: vi.fn(), onClose: vi.fn(), onSaved: vi.fn(), onDeleted: vi.fn(),
});

describe("AgentSettings › A2A remote", () => {
  beforeEach(() => { update.mockClear(); refresh.mockClear(); get.mockClear(); });

  test("tabs are Remote A2A · Mention · Avatar · Advanced, and Persona opens Remote A2A", async () => {
    render(AgentSettings, props("persona"));
    expect(screen.getAllByRole("tab").map((t) => t.textContent)).toEqual(["Remote A2A", "Mention", "Avatar", "Advanced", "Sharing"]);
    expect(screen.getByRole("tab", { name: "Remote A2A" }).getAttribute("aria-selected")).toBe("true");
    expect(screen.getByTestId("remote-card").textContent).toContain("v1.4.0");
    expect(screen.getByTestId("remote-url").textContent).toBe(info.card_url);
    expect(screen.queryByText("System prompt (persona)")).toBeNull();
  });

  test("the stored secret is never shown, only that one is saved", () => {
    render(AgentSettings, props("remote"));
    expect(screen.getByTestId("remote-auth-current").textContent).toContain("Bearer token · secret saved");
    const secret = screen.getByLabelText("Token") as HTMLInputElement;
    expect(secret.value).toBe("");
    expect(secret.placeholder).toContain("saved");
  });

  test("Refresh agent card updates version and skills and reports them up", async () => {
    const p = props("remote");
    render(AgentSettings, p);
    await fireEvent.click(screen.getByTestId("remote-refresh"));
    await waitFor(() => expect(screen.getByTestId("remote-card").textContent).toContain("v1.5.0"));
    expect(screen.getByTestId("remote-card").textContent).toContain("Cite");
    expect(p.onSaved).toHaveBeenCalledWith(expect.objectContaining({ id: "r1", remote: expect.objectContaining({ card: expect.objectContaining({ version: "1.5.0" }) }) }));
  });

  test("changing auth sends the new secret; no separate usage setting", async () => {
    render(AgentSettings, props("remote"));
    await loaded();
    await fireEvent.click(screen.getByRole("button", { name: "API key" }));
    await fireEvent.input(screen.getByLabelText("Key"), { target: { value: "k-1" } });
    await fireEvent.click(screen.getByTestId("remote-auth-save"));
    await waitFor(() => expect(update).toHaveBeenCalledWith("/tools/agents", "r1", { auth: { type: "api_key", header: "X-API-Key", secret: "k-1" } }));
    expect(screen.queryByText("Who may use it")).toBeNull();
  });

  test("Mention says the agent runs outside wick and offers the usual options", async () => {
    render(AgentSettings, props("mention"));
    await waitFor(() => expect(screen.getByTestId("mention-remote-note")).toBeTruthy());
    expect(screen.getByTestId("mention-remote-note").textContent).toContain("runs outside wick");
    expect(screen.getByText("Nobody")).toBeTruthy();
    expect(screen.getByText("Any of my agents")).toBeTruthy();
  });

  test("Advanced edits timeout and max response, refuses out-of-range values, keeps the danger zone", async () => {
    render(AgentSettings, props("advanced"));
    await loaded();
    expect(screen.queryByLabelText("Project")).toBeNull();
    expect(screen.getByText("Danger zone")).toBeDefined();
    const t = screen.getByLabelText("Timeout (seconds)") as HTMLInputElement;
    await fireEvent.input(t, { target: { value: "0" } });
    await fireEvent.change(t);
    expect(screen.getByTestId("remote-limits-error").textContent).toContain("Timeout");
    expect(update).not.toHaveBeenCalled();
    await fireEvent.input(t, { target: { value: "300" } });
    await fireEvent.input(screen.getByLabelText("Max response (KB)"), { target: { value: "4096" } });
    await fireEvent.change(t);
    await waitFor(() => expect(update).toHaveBeenCalledWith("/tools/agents", "r1", { timeout_sec: 300, max_response_bytes: 4194304 }));
  });
});
