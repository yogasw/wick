import { Effect } from "effect";
import { apiGetE, apiPostE } from "@wick-fe/common-api";

/*
 * Live-browser panel API. Routes live under /manager/api/connectors/... (the
 * manager surface), NOT the agents tool base, so URLs are absolute from origin.
 * The key is always "playwright_browser" (the only connector the proxy allows).
 */

const KEY = "playwright_browser";

const root = (connectorId: string) =>
  `/manager/api/connectors/${KEY}/${encodeURIComponent(connectorId)}/browser`;

export type BrowserTab = {
  index: number;
  url: string;
  title: string;
};

export type BrowserSession = {
  session_id: string;
  pid: number;
  browser: string;
  created: string;
  tabs: BrowserTab[] | null;
};

/* Active playwright_browser connector instances, for the dropdown. Reuses the
 * existing rows endpoint; we only need id + label. */
export type BrowserInstance = {
  id: string;
  label: string;
  disabled: boolean;
};

export const listInstances = () =>
  apiGetE<{ rows?: { id: string; label?: string; disabled?: boolean }[] }>(
    `/manager/api/connectors/${KEY}`,
  ).pipe(
    Effect.map((r) =>
      (r.rows ?? []).map((row) => ({
        id: row.id,
        label: row.label?.trim() || row.id,
        disabled: !!row.disabled,
      })),
    ),
  );

export type SessionsResult = { sessions: BrowserSession[]; maxTabs: number };

export const listSessions = (connectorId: string) =>
  apiGetE<{ sessions?: BrowserSession[]; count?: number; max_tabs?: number }>(
    `${root(connectorId)}/sessions`,
  ).pipe(
    Effect.map((r) => ({ sessions: r.sessions ?? [], maxTabs: r.max_tabs ?? 0 }) as SessionsResult),
  );

export const openSession = (connectorId: string) =>
  apiPostE<{ session_id: string }>(`${root(connectorId)}/open`, {});

export const closeSession = (connectorId: string, sessionId: string) =>
  apiPostE<{ session_id: string; closed: boolean }>(
    `${root(connectorId)}/sessions/${encodeURIComponent(sessionId)}/close`,
    {},
  );

export const newTab = (connectorId: string, sessionId: string, url = "") =>
  apiPostE<{ session_id: string; index: number; url: string }>(
    `${root(connectorId)}/sessions/${encodeURIComponent(sessionId)}/tabs/new`,
    { url },
  );

export const closeTab = (connectorId: string, sessionId: string, index: number) =>
  apiPostE<{ session_id: string; closed: number; remaining: number }>(
    `${root(connectorId)}/sessions/${encodeURIComponent(sessionId)}/tabs/${index}/close`,
    {},
  );

/* WebSocket URL for the DevTools proxy. ws(s):// mirrors the page protocol so
 * it works behind TLS. */
export const wsURL = (connectorId: string, sessionId: string, tab: number) => {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const q = new URLSearchParams({ session: sessionId, tab: String(tab) });
  return `${proto}//${location.host}${root(connectorId)}/ws?${q.toString()}`;
};
