import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

let a2aOn = false;
let telegramOn = false;
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  getAgentSlack: () => Promise.resolve({ connected: false, online: false, mode: "socket", dm_main_chat: true, secrets: {}, disabled: false }),
  runApi: <T,>(p: Promise<T>) => p,
}));
vi.mock("../../slackInstant.js", async (orig) => ({
  ...(await orig<typeof import("../../slackInstant.js")>()),
  getAgentSlackInstant: () => Promise.resolve({ enabled: false, bound_channels: [] }),
}));
vi.mock("../../a2aConnection.js", async (orig) => ({
  ...(await orig<typeof import("../../a2aConnection.js")>()),
  getAgentA2A: () => Promise.resolve({ enabled: a2aOn, public_card: false, allowed_callers: [], key_set: a2aOn, endpoint_url: "https://w/a2a", card_url: "https://w/card" }),
}));
vi.mock("../../restConnection.js", async (orig) => ({
  ...(await orig<typeof import("../../restConnection.js")>()),
  getAgentREST: () => Promise.resolve({ enabled: false, base_url: "https://w/v1", model: "agent:rekap", tokens_url: "/tokens" }),
}));
vi.mock("../../telegramConnection.js", async (orig) => ({
  ...(await orig<typeof import("../../telegramConnection.js")>()),
  getAgentTelegram: () => Promise.resolve(telegramOn ? { connected: true, online: false, bot_id: "1", disabled: false } : { connected: false, online: false, disabled: false }),
}));

import AgentConnections from "../AgentConnections.svelte";
import { connState, connTabOf, defaultConnTab } from "../../connectionTabs.js";
import type { AgentItem } from "../../api/team.js";

const agent = { id: "a1", handle: "rekap", name: "Rekap", avatar: { shape: "circle", color: "#6366f1" } } as unknown as AgentItem;
const props = (extra: Record<string, unknown> = {}) => ({ base: "/tools/agents", agent, onClose: vi.fn(), ...extra });
const panel = (k: string) => document.getElementById(`conn-panel-${k}`)!;

describe("AgentConnections tabs", () => {
  test("one tab per channel; nothing connected opens Slack, a click shows only that card and reports it", async () => {
    a2aOn = telegramOn = false;
    const onTab = vi.fn();
    render(AgentConnections, { props: props({ onTab }) });
    await screen.findByTestId("slack-wizard");
    expect(screen.getAllByRole("tab").map((t) => t.textContent?.split("—")[0].trim())).toEqual(["Slack", "Telegram", "A2A", "REST"]);
    expect(screen.getByTestId("conn-tab-slack").getAttribute("aria-selected")).toBe("true");
    expect(panel("slack").hidden).toBe(false);
    expect(panel("a2a").hidden).toBe(true);
    expect(screen.queryByTestId("conn-mark-slack")).toBeNull();
    await fireEvent.click(screen.getByTestId("conn-tab-rest"));
    expect(onTab).toHaveBeenCalledWith("rest");
    expect(panel("rest").hidden).toBe(false);
    expect(panel("slack").hidden).toBe(true);
  });

  test("a connected channel gets ✓, a set-up-but-offline one ⚠, and the first connected tab opens", async () => {
    a2aOn = telegramOn = true;
    render(AgentConnections, { props: props() });
    await waitFor(() => expect(screen.getByTestId("conn-mark-a2a").textContent).toBe("✓"));
    expect(screen.getByTestId("conn-mark-telegram").textContent).toBe("⚠");
    expect(screen.getByTestId("conn-tab-a2a").textContent).toContain("connected");
    expect(screen.queryByTestId("conn-mark-rest")).toBeNull();
    expect(screen.getByTestId("conn-tab-a2a").getAttribute("aria-selected")).toBe("true");
    expect(panel("a2a").hidden).toBe(false);
  });

  test("the URL's conn= picks the tab even when another one is connected", async () => {
    a2aOn = true;
    telegramOn = false;
    render(AgentConnections, { props: props({ tab: "telegram" }) });
    await screen.findByTestId("telegram-card");
    await waitFor(() => expect(screen.getByTestId("conn-mark-a2a")).toBeTruthy());
    expect(screen.getByTestId("conn-tab-telegram").getAttribute("aria-selected")).toBe("true");
    expect(panel("telegram").hidden).toBe(false);
    expect(panel("a2a").hidden).toBe(true);
  });
});

describe("connectionTabs", () => {
  test("state, default tab and conn= parsing", () => {
    expect(connState(true, true)).toBe("on");
    expect(connState(true, false)).toBe("warn");
    expect(connState(false, false, true)).toBe("warn");
    expect(connState(false, false)).toBe("off");
    expect(defaultConnTab({})).toBe("slack");
    expect(defaultConnTab({ slack: "warn", rest: "on", a2a: "on" })).toBe("a2a");
    expect(connTabOf("rest")).toBe("rest");
    expect(connTabOf("fax")).toBeNull();
    expect(connTabOf(null)).toBeNull();
  });
});
