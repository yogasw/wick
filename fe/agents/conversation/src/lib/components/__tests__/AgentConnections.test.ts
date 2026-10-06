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
      { key: "core", label: "Mentions and replies", need: "always", status: "ok", scopes: [{ name: "chat:write", status: "ok" }], events: [{ name: "app_mention", status: "ok" }, { name: "message.channels", status: "pending", hint: "not seen yet — mention the bot or send it a DM to confirm" }], events_from: "received" },
      { key: "dm", label: "Direct messages", need: "always", status: "error", scopes: [{ name: "im:write", status: "error", hint: "in the manifest but not in the token — reinstall the app to your workspace" }], events: [] },
      { key: "reaction_reply", label: "🤖 auto-reply", need: "when_on", status: "off", scopes: [{ name: "reactions:read", status: "off", hint: "off — not checked" }], events: [] },
    ],
  }),
  runApi: <T,>(p: Promise<T>) => p,
}));

let instantNow: { enabled: boolean } & Record<string, unknown> = { enabled: false, bound_channels: [] };
vi.mock("../../slackInstant.js", async (orig) => ({
  ...(await orig<typeof import("../../slackInstant.js")>()),
  getAgentSlackInstant: () => Promise.resolve(instantNow),
  listSlackInstantApps: () => Promise.resolve({ apps: [] }),
}));

let accessFields: { key: string; value: string; type: string; group: string }[] = [];
vi.mock("../../slackAccess.js", async (orig) => ({
  ...(await orig<typeof import("../../slackAccess.js")>()),
  getAgentSlackSettings: () => Promise.resolve({ fields: accessFields, owner_slack_id: "UOWNER", owner_slack_name: "Yoga" }),
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
    // An event not seen yet is neutral (⏳), and the off row says it was not checked.
    expect(matrix.textContent).toContain("⏳ message.channels");
    expect(matrix.textContent).toContain("not seen yet — mention the bot");
    expect(matrix.textContent).toContain("off — not checked");
    expect(screen.getByTestId("events-source").textContent).toContain("received since wick started");
    await fireEvent.click(screen.getByRole("switch", { name: "DMs continue the sender's main chat" }));
    await waitFor(() => expect(updateAgentSlack).toHaveBeenCalledWith("/tools/agents", "a1", { dm_main_chat: false }));
  });
});

describe("AgentConnections Slack mode", () => {
  test("an Instant agent opens on Instant; Custom explains the two are exclusive", async () => {
    current = disconnected;
    instantNow = {
      enabled: true, shared_channel: "slack:__owner__", bound_channels: ["C0123ABCD"], prefix_enabled: false,
      username: "Rekap", avatar_ready: true, shared_online: true, customize_scope: "ok",
    };
    render(AgentConnections, { props: props() });
    await screen.findByTestId("slack-instant");
    expect(screen.getByText("Instant — 1 channel")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Instant (shared app)" }).getAttribute("aria-pressed")).toBe("true");
    await fireEvent.click(screen.getByRole("button", { name: "Custom app" }));
    expect(screen.getByTestId("custom-exclusive").textContent).toContain("Turn off Instant");
    expect(screen.queryByTestId("slack-wizard")).toBeNull();
    instantNow = { enabled: false, bound_channels: [] };
  });

  test("a fresh agent opens on Custom and can switch to Instant", async () => {
    current = disconnected;
    render(AgentConnections, { props: props() });
    await screen.findByTestId("slack-wizard");
    await fireEvent.click(screen.getByRole("button", { name: "Instant (shared app)" }));
    expect(await screen.findByTestId("instant-no-apps")).toBeTruthy();
  });
});

describe("slackConnection", () => {
  test("wizard: each token says where in Slack it comes from", async () => {
    current = disconnected;
    render(AgentConnections, { props: props() });
    await screen.findByTestId("slack-wizard");
    expect(screen.getByTestId("hint-bot").textContent).toBe("OAuth & Permissions › Install to Workspace (or Reinstall) › Bot User OAuth Token.");
    expect(screen.getByTestId("hint-app").textContent).toBe("Basic Information › App-Level Tokens › Generate, scope connections:write.");
    await fireEvent.click(screen.getByRole("button", { name: "HTTP" }));
    expect(screen.getByTestId("hint-sign").textContent).toBe("Basic Information › App Credentials › Signing Secret.");
  });

  test("a stored secret may be left blank; HTTP needs the signing secret", () => {
    const d = { mode: "socket" as const, bot_token: "", app_token: "", signing_secret: "" };
    expect(tokenError(d, connected)).toBe("");
    expect(tokenError(d, disconnected)).toMatch(/xoxb-/);
    expect(tokenError({ ...d, mode: "http", bot_token: "xoxb-1" }, disconnected)).toMatch(/signing secret/);
    expect(connectBody({ ...d, bot_token: " xoxb-2 " })).toEqual({ mode: "socket", bot_token: "xoxb-2" });
    expect(statusLine({ ...connected, disabled: true })).toMatch(/disabled/);
    expect(statusLine(null)).toBe("Not connected");
  });

  test("connected: the header sums up who can use the agent and flags an open app", async () => {
    current = connected;
    const ac = (key: string, value: string) => ({ key, value, type: "dropdown", group: "Access Control" });
    accessFields = [ac("users_mode", "all"), ac("groups_mode", "all"), ac("channels_mode", "all"), ac("bots_mode", "none")];
    render(AgentConnections, { props: props() });
    expect((await screen.findByTestId("slack-access-summary")).textContent).toBe("Access: everyone · all channels");
    expect(screen.getByTestId("slack-open-badge").textContent).toBe("Open to everyone in the workspace");
    accessFields = [];
  });
});
