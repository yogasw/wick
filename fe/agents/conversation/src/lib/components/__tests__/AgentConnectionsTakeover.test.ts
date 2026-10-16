import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { Effect } from "effect";
import { APIError } from "@wick-fe/common-api";
import type { AgentItem, AgentSlackStatus } from "../../api/team.js";

/* The take-over offer rides on the 409 body. Only the transport is
   stubbed here: runApi / runApiRaw are the real ones, so the test fails
   if the APIError (and its body) does not survive to the component. */
const disconnected: AgentSlackStatus = { connected: false, online: false, mode: "socket", dm_main_chat: true, secrets: {}, disabled: false };
const connected: AgentSlackStatus = { ...disconnected, connected: true, online: true, bot_id: "B7", bot_name: "pagebot", secrets: { bot_token: true, app_token: true } };
const offer = {
  error: "this Slack app is already connected to a channel on the Channels page — create a separate Slack app for each agent",
  code: "bot_taken_by_channel",
  instance_key: "slack:u9",
  instance_name: "Slack · @pagebot · Yoga",
  can_take_over: true,
  workflow_refs: [{ workflow_id: "w1", workflow_name: "Inquiry router", kind: "node", id: "post" }],
  sessions: 4,
  session_prefix: "slack-u9-",
  access_note: "Access becomes Only me: only the agent's owner can use the bot until people are added in its Slack settings.",
  rotating: true,
  instant_agents: ["@helper"],
};
const bodies: unknown[] = [];

vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  getAgentSlack: () => Effect.succeed(disconnected),
  connectAgentSlack: (_b: string, _id: string, body: Record<string, unknown>) => {
    bodies.push(body);
    return body.take_over ? Effect.succeed(connected) : Effect.fail(new APIError(409, JSON.stringify(offer)));
  },
  getAgentSlackHealth: () => Effect.succeed({ checks: [], matrix: [] }),
}));
vi.mock("../../slackInstant.js", async (orig) => ({
  ...(await orig<typeof import("../../slackInstant.js")>()),
  getAgentSlackInstant: () => Effect.succeed({ enabled: false, bound_channels: [] }),
}));
vi.mock("../../slackAccess.js", async (orig) => ({
  ...(await orig<typeof import("../../slackAccess.js")>()),
  getAgentSlackSettings: () => Effect.succeed({ fields: [], owner_slack_id: "", owner_slack_name: "" }),
}));

import AgentConnections from "../AgentConnections.svelte";

const agent = { id: "a1", handle: "rekap", name: "Rekap", avatar: { shape: "circle", color: "#6366f1" } } as unknown as AgentItem;

describe("AgentConnections take over", () => {
  test("a 409 bot_taken_by_channel shows the impact and Take over sends take_over:true", async () => {
    render(AgentConnections, { props: { base: "/tools/agents", agent, onClose: vi.fn() } });
    await screen.findByTestId("slack-wizard");
    await fireEvent.input(screen.getByLabelText("Bot token (xoxb-…)"), { target: { value: "xoxb-x-B7" } });
    await fireEvent.input(screen.getByLabelText("App token (xapp-… or xoxe.xapp-…)"), { target: { value: "xapp-1" } });
    await fireEvent.click(screen.getByRole("button", { name: "Connect & test" }));

    const panel = await screen.findByTestId("slack-takeover");
    expect(panel.textContent).toContain("slack:u9");
    const impact = screen.getByTestId("slack-takeover-impact").textContent ?? "";
    expect(impact).toContain("4 thread sessions");
    expect(impact).toContain("Inquiry router");
    expect(impact).toContain("Only me");
    expect(impact).toContain("rotating app token");
    expect(impact).toContain("@helper (Instant)");
    expect(screen.queryByTestId("connect-error")).toBeNull();

    await fireEvent.click(screen.getByRole("button", { name: "Take over from Channels page" }));
    await waitFor(() => expect(bodies.length).toBe(2));
    expect(bodies[1]).toMatchObject({ take_over: true, bot_token: "xoxb-x-B7" });
    await waitFor(() => expect(screen.queryByTestId("slack-takeover")).toBeNull());
  });
});
