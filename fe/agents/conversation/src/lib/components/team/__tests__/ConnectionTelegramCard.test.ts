import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { AgentTelegramStatus } from "../../../telegramConnection.js";

const empty: AgentTelegramStatus = { connected: false, online: false, disabled: false };
const live: AgentTelegramStatus = {
  connected: true, online: true, disabled: false, bot_id: "111", bot_username: "rekap_bot", link: "https://t.me/rekap_bot",
};
let current: AgentTelegramStatus = empty;
let failConnect = false;
const connect = vi.fn((_b: string, _id: string, _tok: string) => {
  if (failConnect) return Promise.reject(new Error("this Telegram bot is already connected to @loki"));
  current = live;
  return Promise.resolve(live);
});
const test_ = vi.fn(() => Promise.resolve({ ok: true, detail: "@rekap_bot answers", bot_username: "rekap_bot" }));
const disconnect = vi.fn(() => { current = empty; return Promise.resolve({ status: "disconnected" }); });
vi.mock("../../../telegramConnection.js", async (orig) => ({
  ...(await orig<typeof import("../../../telegramConnection.js")>()),
  getAgentTelegram: () => Promise.resolve(current),
  connectAgentTelegram: (b: string, id: string, tok: string) => connect(b, id, tok),
  testAgentTelegram: () => test_(),
  disconnectAgentTelegram: () => disconnect(),
}));
vi.mock("../../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/team.js")>()),
  runApi: <T,>(p: Promise<T>) => p,
}));

import ConnectionTelegramCard from "../ConnectionTelegramCard.svelte";
import type { AgentItem } from "../../../api/team.js";

const agent = { id: "a1", handle: "rekap" } as unknown as AgentItem;
const TOKEN = "111:AAHsecretsecretsecretsecret";

describe("ConnectionTelegramCard", () => {
  beforeEach(() => {
    connect.mockClear(); test_.mockClear(); disconnect.mockClear();
    current = empty; failConnect = false;
  });

  test("pastes the token once, then shows the bot and never the token", async () => {
    const { container } = render(ConnectionTelegramCard, { base: "/tools/agents", agent });
    const input = (await screen.findByTestId("telegram-token")) as HTMLInputElement;
    expect(input.type).toBe("password");
    await fireEvent.input(input, { target: { value: TOKEN } });
    await fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    await waitFor(() => expect(connect).toHaveBeenCalledWith("/tools/agents", "a1", TOKEN));
    const link = await screen.findByTestId("telegram-link");
    expect(link.getAttribute("href")).toBe("https://t.me/rekap_bot");
    expect(link.textContent).toBe("@rekap_bot");
    expect(screen.getByTestId("telegram-badge").textContent).toBe("Online");
    expect(container.innerHTML).not.toContain("AAHsecret");
    expect(screen.queryByTestId("telegram-token")).toBeNull();
  });

  test("a refused token shows the server's reason and clears the field", async () => {
    failConnect = true;
    render(ConnectionTelegramCard, { base: "/tools/agents", agent });
    const input = (await screen.findByTestId("telegram-token")) as HTMLInputElement;
    await fireEvent.input(input, { target: { value: TOKEN } });
    await fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    expect((await screen.findByTestId("telegram-error")).textContent).toContain("@loki");
    await waitFor(() => expect((screen.getByTestId("telegram-token") as HTMLInputElement).value).toBe(""));
  });

  test("Test reports and Disconnect returns to the token form", async () => {
    current = live;
    render(ConnectionTelegramCard, { base: "/tools/agents", agent });
    await screen.findByTestId("telegram-link");
    await fireEvent.click(screen.getByRole("button", { name: "Test" }));
    expect((await screen.findByTestId("telegram-test")).textContent).toContain("@rekap_bot answers");
    await fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
    await waitFor(() => expect(disconnect).toHaveBeenCalled());
    expect(await screen.findByTestId("telegram-token")).toBeTruthy();
  });

  test("a disabled agent's bot reads as such", async () => {
    current = { ...live, online: false, disabled: true };
    render(ConnectionTelegramCard, { base: "/tools/agents", agent });
    expect((await screen.findByTestId("telegram-badge")).textContent).toBe("Agent disabled");
  });
});
