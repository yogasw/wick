import { apiGetE, apiPostE } from "@wick-fe/common-api";
import type { Status, RouterInfo } from "./types.js";

// Control endpoints hang off the agents tool base (e.g. /tools/agents), under
// the /airouter/<id>/* subtree — one router per id. They return JSON and are
// admin-gated server-side. The autostart/external toggles read c.Form("on"),
// and Go's FormValue also reads the URL query, so we pass ?on=... rather than a
// form body (keeps the JSON client happy — no body sent).
//
// Per the fe-module contract these expose the Effect (no layer provided); the
// consumer runs them with WickClientLayer, tests swap in a mock HttpClient.

// fetchRouters lists every registered router so the switcher can enumerate them.
export const fetchRouters = (base: string) =>
  apiGetE<{ routers: RouterInfo[] }>(`${base}/airouter/routers`);

export const fetchStatus = (base: string, id: string) =>
  apiGetE<Status>(`${base}/airouter/${id}/status`);

export const install = (base: string, id: string) =>
  apiPostE<{ version: string }>(`${base}/airouter/${id}/install`);

export const start = (base: string, id: string) =>
  apiPostE<{ status: string }>(`${base}/airouter/${id}/start`);

export const stop = (base: string, id: string) =>
  apiPostE<{ status: string }>(`${base}/airouter/${id}/stop`);

export const restart = (base: string, id: string) =>
  apiPostE<{ status: string }>(`${base}/airouter/${id}/restart`);

export const setAutostart = (base: string, id: string, on: boolean) =>
  apiPostE<{ autostart: boolean }>(`${base}/airouter/${id}/autostart?on=${on ? "true" : "false"}`);

export const setExternal = (base: string, id: string, on: boolean) =>
  apiPostE<{ external: boolean }>(`${base}/airouter/${id}/external?on=${on ? "true" : "false"}`);

// reqStreamURL is the SSE endpoint the Requests tab connects to. Being
// connected is what tells the server to start capturing bodies.
export const reqStreamURL = (base: string, id: string): string =>
  `${base}/airouter/${id}/reqstream`;

// logStreamURL is the SSE endpoint the Settings tab connects to for live
// process output: a snapshot on connect, then incremental chunks.
export const logStreamURL = (base: string, id: string): string =>
  `${base}/airouter/${id}/logstream`;

// dashboardURL is the iframe src for a router's proxied dashboard — mounted at
// the wick root (not under the tool base) so its root-absolute URLs resolve.
export const dashboardURL = (id: string): string => `/airouter/${id}/`;

// LOG_RESET is the sentinel the server sends when the process (re)starts,
// telling the client to clear its accumulated log view.
export const LOG_RESET = "\x00__reset__\x00";
