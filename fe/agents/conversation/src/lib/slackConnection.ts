/* Connections › Slack: the pure half of AgentConnections.svelte. */
import type { AgentSlackStatus, SlackMatrixStatus, SlackSecretKey } from "./api/team.js";

export const MATRIX_ICON: Record<SlackMatrixStatus, string> = { ok: "✅", warn: "⚠️", error: "❌", off: "·", pending: "⏳" };
export const MATRIX_LABEL: Record<SlackMatrixStatus, string> = { ok: "ok", warn: "warning", error: "missing", off: "off", pending: "not seen yet" };

/** eventsSourceNote says how the matrix judged events. */
export function eventsSourceNote(from: string | undefined): string {
  return from === "manifest"
    ? "Events checked from the app manifest."
    : "Events checked from events received since wick started — ⏳ means not seen yet, not missing.";
}

/** What the masked field shows for a secret the server holds. */
export const MASKED = "••••••••";

/** TOKEN_HINTS says where in the Slack app's settings each secret lives. */
export const TOKEN_HINTS = {
  bot_token: "OAuth & Permissions › Install to Workspace (or Reinstall) › Bot User OAuth Token.",
  app_token: "Basic Information › App-Level Tokens › Generate, scope connections:write.",
  signing_secret: "Basic Information › App Credentials › Signing Secret.",
  rotation: "Basic Information → App Credentials. wick refreshes the 12-hour token by itself.",
} as const;

export type TokenDraft = {
  mode: "socket" | "http";
  bot_token: string;
  app_token: string;
  signing_secret: string;
  app_refresh_token: string;
  client_id: string;
  client_secret: string;
};

/** isRotatingAppToken: an app token from token rotation (xoxe.xapp-),
    which expires after 12 hours and is refreshed by wick. */
export const isRotatingAppToken = (s: string) => s.trim().startsWith("xoxe.xapp-");

/** showRotation: the refresh token / client fields matter — a rotating
    app token is being pasted, or the stored one rotates and is kept. */
export function showRotation(d: TokenDraft, st: AgentSlackStatus | null): boolean {
  if (d.mode !== "socket") return false;
  const app = d.app_token.trim();
  return app ? app.startsWith("xoxe.") : !!st?.rotating;
}

/** tokenError is why step 2 cannot connect yet, "" when it can. A blank
    field is fine when the server already holds that secret — except the
    refresh token of a newly pasted rotating app token, which only works
    with the refresh token issued alongside it. */
export function tokenError(d: TokenDraft, st: AgentSlackStatus | null): string {
  const has = (k: SlackSecretKey) => !!st?.secrets?.[k];
  const bot = d.bot_token.trim();
  if (bot ? !/^(xoxe\.)?xoxb-/.test(bot) : !has("bot_token")) return "Paste the Bot User OAuth Token (starts with xoxb- or xoxe.xoxb-).";
  if (d.mode === "socket") {
    const app = d.app_token.trim();
    if (app ? !/^(xoxe\.)?xapp-/.test(app) : !has("app_token")) return "Socket mode needs an app-level token (starts with xapp- or xoxe.xapp-) with connections:write.";
    if (!showRotation(d, st)) return "";
    const refresh = d.app_refresh_token.trim();
    if (refresh ? !refresh.startsWith("xoxe-") : app || !has("app_refresh_token")) {
      return "A rotating app token (xoxe.xapp-…) needs the refresh token issued with it (starts with xoxe-).";
    }
    if (!d.client_id.trim() && !st?.client_id) return "A rotating app token needs the app's Client ID.";
    if (!d.client_secret.trim() && !has("client_secret")) return "A rotating app token needs the app's Client Secret.";
  } else if (!d.signing_secret.trim() && !has("signing_secret")) {
    return "HTTP mode needs the app's signing secret.";
  }
  return "";
}

/** connectBody sends only what was typed: a blank secret keeps the stored one. */
export function connectBody(d: TokenDraft): Record<string, string> {
  const out: Record<string, string> = { mode: d.mode };
  for (const k of ["bot_token", "app_token", "signing_secret", "app_refresh_token", "client_id", "client_secret"] as const) {
    const v = d[k].trim();
    if (v) out[k] = v;
  }
  return out;
}

/** formatExpiry: "expires in 11h 40m", "expired", "" when unknown. */
export function formatExpiry(expiresAt: string | undefined, now: Date = new Date()): string {
  const t = expiresAt ? Date.parse(expiresAt) : NaN;
  if (Number.isNaN(t)) return "";
  const mins = Math.floor((t - now.getTime()) / 60000);
  if (mins <= 0) return "expired";
  const h = Math.floor(mins / 60);
  return h ? `expires in ${h}h ${mins % 60}m` : `expires in ${mins}m`;
}

/** rotationLine is the badge of a rotating app token, "" when it does not rotate. */
export function rotationLine(st: AgentSlackStatus | null, now: Date = new Date()): string {
  if (!st?.rotating) return "";
  const parts = ["Rotating"];
  const exp = formatExpiry(st.app_token_expires_at, now);
  if (exp) parts.push(exp);
  const last = st.last_refresh ? new Date(st.last_refresh) : null;
  if (last && !Number.isNaN(last.getTime())) {
    const pad = (n: number) => String(n).padStart(2, "0");
    parts.push(`last refreshed ${pad(last.getHours())}:${pad(last.getMinutes())}`);
  }
  return parts.join(" · ");
}

/** statusLine is the one-line state under the Slack card title. */
export function statusLine(st: AgentSlackStatus | null): string {
  if (!st || !st.connected) return "Not connected";
  if (st.disabled) return "Offline — the agent is disabled";
  const who = st.bot_name ? `@${st.bot_name}` : "bot";
  const where = st.team_name ? ` in ${st.team_name}` : "";
  return st.online ? `Online as ${who}${where}` : `Connected as ${who}${where} · offline`;
}
