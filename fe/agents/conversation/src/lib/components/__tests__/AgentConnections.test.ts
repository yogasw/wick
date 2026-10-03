import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { AgentSlackStatus } from "../../api/team.js";

const disconnected: AgentSlackStatus = { connected: false, online: false, mode: "socket", dm_main_chat: true, secrets: {}, disabled: false };
const connected: AgentSlackStatus = {
  connected: true, online: true, mode: "socket", bot_id: "B1", bot_name: "rekap", team_name: "Acme",
  dm_main_chat: true, secrets: { bot_token: true, app_token: true }, disabled: false,
};
let current: AgentSlackStatus = disconnected;
const connectAgentSlack = vi.fn((_b: string, _id: string, _body: unknown) => Promise.resolve(connected));
const updateAgentSlack = vi.fn((_b: string, _id: string, body: { dm_main_chat?: boolean }) =>
  Promise.resolve({ ...connected, dm_main_chat: !!body.dm_main_chat }));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  getAgentSlack: () => Promise.resolve(current),
  connectAgentSlack: (b: string, id: string, body: unknown) => connectAgentSlack(b, id, body),
  updateAgentSlack: (b: string, id: string, body: { dm_main_chat?: boolean }) => updateAgentSlack(b, id, body),
  disconnectAgentSlack: () => Promise.resolve({}),
  getAgentSlackManifest: () => Promise.resolve({ manifest: { display_information: { name: "Rekap" } }, create_url: "https://api.slack.com/apps?new_app=1&manifest_json=%7B%7D" }),
  getAgentSlackHealth: () => Promise.resolve({
    checks: [{ name: "auth.test", ok: true }],
    matrix: [
      { key: "core", label: "Mentions and replies", need: "always", status: "ok", scopes: [{ name: "chat:write", status: "ok" }], events: [{ name: "app_mention", status: "ok" }] },
      { key: "dm", label: "Direct messages", need: "always", status: "error", scopes: [{ name: "im:write", status: "error", hint: "in the manifest but not in the token — reinstall the app to your workspace" }], events: [] },
      { key: "reaction_reply", label: "🤖 auto-reply", need: "when_on", status: "off", scopes: [{ name: "reactions:read", status: "off" }], events: [] },
    ],
  }),
  runApi: <T,>(p: Promise<T>) => p,
}));

import AgentConnections from "../AgentConnections.svelte";
import { connectBody, statusLine, tokenError } from "../../slackConnection.js";
import type { AgentItem } from "../../api/team.js";

const agent = { id: "a1", handle: "rekap", name: "Rekap", avatar: { shape: "circle", color: "#6366f1" } } as unknown as AgentItem;
const props = () => ({ base: "/tools/agents", agent, onClose: vi.fn() });

describe("AgentConnections", () => {
  beforeEach(() => { connectAgentSlack.mockClear(); updateAgentSlack.mockClear(); });

  test("wizard: connect stays disabled until the tokens look right, then sends only what was typed", async () => {
    current = disconnected;
    render(AgentConnections, { props: props() });
    await screen.findByTestId("slack-wizard");
    expect(screen.getByText("Create the Slack app ↗")).toBeTruthy();
    const btn = screen.getByRole("button", { name: "Connect & test" }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    await fireEvent.input(screen.getByLabelText("Bot token (xoxb-…)"), { target: { value: "xoxb-1" } });
    expect(screen.getByTestId("token-problem").textContent).toMatch(/xapp-/);
    await fireEvent.input(screen.getByLabelText("App token (xapp-…)"), { target: { value: "xapp-1" } });
    await fireEvent.click(btn);
    await waitFor(() => expect(connectAgentSlack).toHaveBeenCalled());
    expect(connectAgentSlack.mock.calls[0][2]).toEqual({ mode: "socket", bot_token: "xoxb-1", app_token: "xapp-1" });
    expect(await screen.findByTestId("slack-matrix")).toBeTruthy();
  });

  test("connected: secrets are masked, the matrix shows reinstall hints and off rows, the DM option autosaves", async () => {
    current = connected;
    render(AgentConnections, { props: props() });
    await screen.findByText("Online as @rekap in Acme");
    expect(screen.getByTestId("secret-bot_token").textContent).toContain("••••");
    expect(document.body.textContent).not.toContain("xoxb-");
    const matrix = await screen.findByTestId("slack-matrix");
    expect(matrix.textContent).toContain("reinstall the app");
    expect(matrix.querySelector('[data-status="off"]')).toBeTruthy();
    await fireEvent.click(screen.getByRole("switch", { name: "DMs continue the sender's main chat" }));
    await waitFor(() => expect(updateAgentSlack).toHaveBeenCalledWith("/tools/agents", "a1", { dm_main_chat: false }));
  });
});

describe("slackConnection", () => {
  test("a stored secret may be left blank; HTTP needs the signing secret", () => {
    const d = { mode: "socket" as const, bot_token: "", app_token: "", signing_secret: "" };
    expect(tokenError(d, connected)).toBe("");
    expect(tokenError(d, disconnected)).toMatch(/xoxb-/);
    expect(tokenError({ ...d, mode: "http", bot_token: "xoxb-1" }, disconnected)).toMatch(/signing secret/);
    expect(connectBody({ ...d, bot_token: " xoxb-2 " })).toEqual({ mode: "socket", bot_token: "xoxb-2" });
    expect(statusLine({ ...connected, disabled: true })).toMatch(/disabled/);
    expect(statusLine(null)).toBe("Not connected");
  });
});
