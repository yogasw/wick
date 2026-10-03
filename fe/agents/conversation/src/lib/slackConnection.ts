/* Connections › Slack: the pure half of AgentConnections.svelte. */
import type { AgentSlackStatus, SlackMatrixStatus } from "./api/team.js";

export const MATRIX_ICON: Record<SlackMatrixStatus, string> = { ok: "✅", warn: "⚠️", error: "❌", off: "·" };
export const MATRIX_LABEL: Record<SlackMatrixStatus, string> = { ok: "ok", warn: "warning", error: "missing", off: "off" };

/** What the masked field shows for a secret the server holds. */
export const MASKED = "••••••••";

export type TokenDraft = { mode: "socket" | "http"; bot_token: string; app_token: string; signing_secret: string };

/** tokenError is why step 2 cannot connect yet, "" when it can. A blank
    field is fine when the server already holds that secret. */
export function tokenError(d: TokenDraft, st: AgentSlackStatus | null): string {
  const has = (k: "bot_token" | "app_token" | "signing_secret") => !!st?.secrets?.[k];
  const bot = d.bot_token.trim();
  if (bot ? !bot.startsWith("xoxb-") : !has("bot_token")) return "Paste the Bot User OAuth Token (starts with xoxb-).";
  if (d.mode === "socket") {
    const app = d.app_token.trim();
    if (app ? !app.startsWith("xapp-") : !has("app_token")) return "Socket mode needs an app-level token (starts with xapp-) with connections:write.";
  } else if (!d.signing_secret.trim() && !has("signing_secret")) {
    return "HTTP mode needs the app's signing secret.";
  }
  return "";
}

/** connectBody sends only what was typed: a blank secret keeps the stored one. */
export function connectBody(d: TokenDraft): Record<string, string> {
  const out: Record<string, string> = { mode: d.mode };
  for (const k of ["bot_token", "app_token", "signing_secret"] as const) {
    const v = d[k].trim();
    if (v) out[k] = v;
  }
  return out;
}

/** statusLine is the one-line state under the Slack card title. */
export function statusLine(st: AgentSlackStatus | null): string {
  if (!st || !st.connected) return "Not connected";
  if (st.disabled) return "Offline — the agent is disabled";
  const who = st.bot_name ? `@${st.bot_name}` : "bot";
  const where = st.team_name ? ` in ${st.team_name}` : "";
  return st.online ? `Online as ${who}${where}` : `Connected as ${who}${where} · offline`;
}
