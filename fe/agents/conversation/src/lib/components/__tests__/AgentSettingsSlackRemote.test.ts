import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { AgentItem, SlackRemoteInfo } from "../../api/team.js";

const info: SlackRemoteInfo = {
  connector_id: "slk1", identity: "bot", target: "channel", channel: "C0OPS", target_name: "#ops", listen: "target",
  marker: true, mention_target: true, idle_sec: 0, max_sec: 0, updated_at: "2026-10-03T10:00:00Z",
  idle_sec_effective: 20, max_sec_effective: 180, listen_effective: "target", usage_effective: "only_me",
  warning: "Messages you send to this agent, including other agents' output that mentions it, will be posted to #ops in Slack.",
};
const get = vi.fn(() => Promise.resolve(info));
const update = vi.fn((_b: string, _id: string, body: Record<string, unknown>) =>
  Promise.resolve({ ...info, ...body, usage_effective: (body.usage as string) ?? info.usage_effective }));
const test_ = vi.fn((_b: string, _body: unknown) => Promise.resolve({ ok: false, state: "no_reply", latency_ms: 30000 }));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  getSlackRemote: () => get(),
  updateSlackRemote: (b: string, id: string, body: Record<string, unknown>) => update(b, id, body),
  testSlackRemote: (b: string, body: unknown) => test_(b, body),
  getSlackIdentities: () => Promise.resolve({ bot: true, accounts: [] }),
  updateAgent: (_b: string, _id: string, body: unknown) => Promise.resolve({ ...slack, ...(body as object) }),
  listAgentConnectors: () => Promise.resolve([{ id: "slk1", key: "slack", label: "Acme Slack", description: "", accounts: [], ops: [] }]),
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

const slack = {
  id: "s1", handle: "ops", name: "ops", is_captain: false, kind: "slack-remote", slack_remote: info,
  project_id: "p1", icon: "", description: "", system_prompt: "", provider: "slack-remote/slack-remote", model: "", preset: "",
  features: {}, avatar: { shape: "circle", color: "#6366f1" }, allowed_connectors: [], allowed_native_tools: [],
  include_new_connectors: false, disabled: false, main_session_id: "", last_active: null, last_preview: "", status: "idle",
} as unknown as AgentItem;
const loaded = async () => { await waitFor(() => expect(get).toHaveBeenCalled()); await new Promise((r) => setTimeout(r, 0)); };
const props = (tab: SettingsTab) => ({
  base: "/tools/agents", agent: slack, agents: [slack], tab, onTab: vi.fn(), onClose: vi.fn(), onSaved: vi.fn(), onDeleted: vi.fn(),
});

describe("AgentSettings › Slack remote", () => {
  beforeEach(() => { update.mockClear(); get.mockClear(); test_.mockClear(); });

  // A remote agent has its own Persona tab now, so "persona" opens it;
  // the Slack target is on the Remote tab.
  test("tabs are Remote · Persona · Mention · Avatar · Advanced, and Persona opens Persona", async () => {
    const first = render(AgentSettings, props("persona"));
    expect(screen.getAllByRole("tab").map((t) => t.textContent)).toEqual(["Remote", "Persona", "Mention", "Avatar", "Advanced", "Sharing"]);
    expect(screen.getByRole("tab", { name: "Persona" }).getAttribute("aria-selected")).toBe("true");
    first.unmount();
    render(AgentSettings, props("remote"));
    expect(screen.getByRole("tab", { name: "Remote" }).getAttribute("aria-selected")).toBe("true");
    expect(screen.getByTestId("slack-remote-settings")).toBeTruthy();
    expect(screen.getByTestId("slack-remote-warning").textContent).toContain("posted to #ops");
    expect((screen.getByLabelText("Channel ID") as HTMLInputElement).value).toBe("C0OPS");
    await waitFor(() => expect(screen.getByText("Acme Slack")).toBeTruthy());
    expect(screen.queryByText("System prompt (persona)")).toBeNull();
  });

  test("Save sends the whole target with cleared fields blank; Test runs on the saved agent", async () => {
    const p = props("remote");
    render(AgentSettings, p);
    await loaded();
    expect((screen.getByTestId("ss-save") as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.click(screen.getByRole("button", { name: "DM" }));
    await fireEvent.input(screen.getByLabelText("User or bot ID"), { target: { value: "U0HELP" } });
    await fireEvent.input(screen.getByLabelText("Display name"), { target: { value: "" } });
    await fireEvent.click(screen.getByTestId("ss-marker"));
    // Always @mention starts on and can be switched off.
    expect((screen.getByTestId("ss-mention-target") as HTMLInputElement).checked).toBe(true);
    expect(screen.getByText(/Each message starts with @target so bots that only answer mentions are triggered/)).toBeDefined();
    await fireEvent.click(screen.getByTestId("ss-mention-target"));
    await fireEvent.input(screen.getByLabelText("Max (seconds)"), { target: { value: "600" } });
    expect((screen.getByTestId("ss-test") as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.click(screen.getByTestId("ss-save"));
    await waitFor(() => expect(update).toHaveBeenCalledWith("/tools/agents", "s1", {
      connector_id: "slk1", identity: "bot", target: "dm", user: "U0HELP", listen: "target", marker: false, mention_target: false, idle_sec: 0, max_sec: 600,
      account_id: "", channel: "", mention_id: "", thread_ts: "", target_name: "",
    }));
    expect(p.onSaved).toHaveBeenCalledWith(expect.objectContaining({ id: "s1", slack_remote: expect.objectContaining({ target: "dm" }) }));
    await fireEvent.click(screen.getByTestId("ss-test"));
    await waitFor(() => expect(screen.getByTestId("ss-test-result").textContent).toContain("Ping posted, no reply yet"));
    expect(test_).toHaveBeenCalledWith("/tools/agents", { agent_id: "s1" });
  });

  test("Advanced keeps the danger zone; who may use it lives in Mention", async () => {
    render(AgentSettings, props("advanced"));
    await loaded();
    expect(screen.getByText("Danger zone")).toBeDefined();
    expect(screen.queryByText("Who may use it")).toBeNull();
    await fireEvent.click(screen.getByRole("tab", { name: "Mention" }));
  });
});
