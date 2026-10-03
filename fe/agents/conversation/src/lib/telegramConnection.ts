import { apiDeleteE, apiGetE, apiPostE, apiPutE } from "@wick-fe/common-api";

/* Connections › Telegram (api_team_telegram.go). The agent's own bot: the
   BotFather token is sent once on connect and never comes back — status
   carries the bot's username and t.me link only. */
export type AgentTelegramStatus = {
  connected: boolean;
  online: boolean;
  bot_id?: string;
  bot_username?: string;
  link?: string;
  disabled: boolean;
};
export type TelegramTestResult = { ok: boolean; detail: string; bot_username?: string };

const enc = encodeURIComponent;
const path = (base: string, id: string) => `${base}/api/team/agents/${enc(id)}/telegram`;

export const getAgentTelegram = (base: string, id: string) => apiGetE<AgentTelegramStatus>(path(base, id));
export const connectAgentTelegram = (base: string, id: string, botToken: string) =>
  apiPutE<AgentTelegramStatus>(path(base, id), { bot_token: botToken });
export const testAgentTelegram = (base: string, id: string) => apiPostE<TelegramTestResult>(`${path(base, id)}/test`, {});
export const disconnectAgentTelegram = (base: string, id: string) => apiDeleteE<{ status: string }>(path(base, id));
