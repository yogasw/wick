import { apiGetE, apiPostE } from "@wick-fe/common-api";
import type { Status } from "./types.js";

// All control endpoints hang off the agents tool base (e.g. /tools/agents),
// under the /9router/* subtree. They return JSON on success and are gated
// admin-only server-side. The autostart/external toggles read c.Form("on"),
// and Go's FormValue also reads the URL query — so we pass ?on=... rather
// than a form body, which keeps the JSON client happy (no body sent).
//
// Per the fe-module contract, these expose the Effect (no layer provided);
// the consumer runs them with WickClientLayer, and tests swap in a mock
// HttpClient layer.

export const fetchStatus = (base: string) => apiGetE<Status>(`${base}/9router/status`);

export const install = (base: string) => apiPostE<{ version: string }>(`${base}/9router/install`);

export const start = (base: string) => apiPostE<{ status: string }>(`${base}/9router/start`);

export const stop = (base: string) => apiPostE<{ status: string }>(`${base}/9router/stop`);

export const restart = (base: string) => apiPostE<{ status: string }>(`${base}/9router/restart`);

export const setAutostart = (base: string, on: boolean) =>
  apiPostE<{ autostart: boolean }>(`${base}/9router/autostart?on=${on ? "true" : "false"}`);

export const setExternal = (base: string, on: boolean) =>
  apiPostE<{ external: boolean }>(`${base}/9router/external?on=${on ? "true" : "false"}`);

// reqStreamURL is the SSE endpoint the Requests tab connects to. Being
// connected is what tells the server to start capturing bodies.
export const reqStreamURL = (base: string): string => `${base}/9router/reqstream`;

// logStreamURL is the SSE endpoint the Settings tab connects to for live
// 9router process output: a snapshot on connect, then incremental chunks.
export const logStreamURL = (base: string): string => `${base}/9router/logstream`;

// LOG_RESET is the sentinel the server sends when the process (re)starts,
// telling the client to clear its accumulated log view.
export const LOG_RESET = "\x00__reset__\x00";
