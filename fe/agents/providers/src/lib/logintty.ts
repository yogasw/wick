/* Reconnect (login TTY) API layer: connect-status + usage for one
   provider instance, session lifecycle (start / extend / kill), and the
   websocket URL + small view helpers for the terminal modal. */

import { get, post, ApiError } from "./api.js";

export type LoginAccount = {
  connected: boolean;
  email: string;
  plan: string;
  org: string;
  authMethod: string;
  expiresAt: string; // RFC3339 or ""
};

export type LoginTTYSession = {
  id: string;
  state: string; // running | exited | expired | killed
  remainingS: number;
  capS: number;
};

export type LoginTTYStatus = {
  supported: boolean;
  account: LoginAccount;
  session: LoginTTYSession | null;
  defaultTtlS: number;
  extendS: number;
  maxTtlS: number;
};

export type UsageWindow = {
  key: string;
  utilization: number; // percent 0-100
  resetsAt: string;
};

export type UsageResult = {
  supported: boolean;
  windows: UsageWindow[];
  error: string;
  /* pending = the first probe for this account is still queued behind
     the server's pacing gate; not an error. */
  pending: boolean;
  /* checking = a probe for this account is in flight right now. */
  checking: boolean;
  /* Provenance of a cached reading: see ProviderConnection. */
  fetchedAt: string;
  ageS: number;
  nextS: number;
};

/* One frame on the login TTY websocket (server → client). */
export type LoginTTYFrame = {
  t: "out" | "link" | "success" | "fail" | "ttl" | "state";
  data?: string;
  url?: string;
  line?: string;
  remaining_s?: number;
  cap_s?: number;
  state?: string;
  exit_err?: string;
  account?: WireLoginAccount;
};

interface WireLoginAccount {
  connected?: boolean;
  email?: string;
  plan?: string;
  org?: string;
  auth_method?: string;
  expires_at?: string;
}

interface WireLoginSession {
  id?: string;
  state?: string;
  remaining_s?: number;
  cap_s?: number;
}

interface WireLoginStatus {
  supported?: boolean;
  account?: WireLoginAccount | null;
  session?: WireLoginSession | null;
  default_ttl_s?: number;
  extend_s?: number;
  max_ttl_s?: number;
}

interface WireUsage {
  supported?: boolean;
  windows?: Array<{ key?: string; utilization?: number; resets_at?: string }> | null;
  error?: string;
  pending?: boolean;
  checking?: boolean;
  fetched_at?: string;
  age_s?: number;
  next_s?: number;
}

export function mapLoginAccount(w: WireLoginAccount | null | undefined): LoginAccount {
  return {
    connected: w?.connected ?? false,
    email: w?.email ?? "",
    plan: w?.plan ?? "",
    org: w?.org ?? "",
    authMethod: w?.auth_method ?? "",
    expiresAt: w?.expires_at ?? "",
  };
}

function mapSession(w: WireLoginSession | null | undefined): LoginTTYSession | null {
  if (!w || !w.id) return null;
  return {
    id: w.id,
    state: w.state ?? "",
    remainingS: w.remaining_s ?? 0,
    capS: w.cap_s ?? 0,
  };
}

export function normalizeLoginStatus(w: WireLoginStatus): LoginTTYStatus {
  return {
    supported: w.supported ?? false,
    account: mapLoginAccount(w.account),
    session: mapSession(w.session),
    defaultTtlS: w.default_ttl_s ?? 300,
    extendS: w.extend_s ?? 300,
    maxTtlS: w.max_ttl_s ?? 1800,
  };
}

export function normalizeUsage(w: WireUsage): UsageResult {
  return {
    supported: w.supported ?? false,
    windows: (w.windows ?? []).map((x) => ({
      key: x.key ?? "",
      utilization: x.utilization ?? 0,
      resetsAt: x.resets_at ?? "",
    })),
    error: w.error ?? "",
    pending: w.pending ?? false,
    checking: w.checking ?? false,
    fetchedAt: w.fetched_at ?? "",
    ageS: w.age_s ?? 0,
    nextS: w.next_s ?? 0,
  };
}

function ttyPath(base: string, type: string, name: string): string {
  return `${base}/api/providers/${encodeURIComponent(type)}/${encodeURIComponent(name)}/logintty`;
}

export async function apiLoginTTYStatus(base: string, type: string, name: string): Promise<LoginTTYStatus> {
  const r = await get<WireLoginStatus>(ttyPath(base, type, name));
  return normalizeLoginStatus(r);
}

export async function apiLoginTTYUsage(base: string, type: string, name: string): Promise<UsageResult> {
  const r = await get<WireUsage>(`${ttyPath(base, type, name)}/usage`);
  return normalizeUsage(r);
}

/* UsageRefreshResult is the answer to a re-check request.

   accepted=false is NOT an error: the server refused because a probe
   would land inside a cooldown (its own rate-limit floor, or one the
   upstream endpoint asked for). waitS says how long, so the button can
   explain itself instead of looking broken. */
export type UsageRefreshResult = { accepted: boolean; checking: boolean; waitS: number };

/* apiLoginTTYUsageRefresh asks for a fresh reading of this account now,
   dropping the cache TTL. Safe to call on a card that already has
   numbers — the server decides whether a probe may actually go out. */
export async function apiLoginTTYUsageRefresh(
  base: string,
  type: string,
  name: string,
): Promise<UsageRefreshResult> {
  const r = await post<{ accepted?: boolean; checking?: boolean; wait_s?: number }>(
    `${ttyPath(base, type, name)}/usage/refresh`,
  );
  return { accepted: r?.accepted ?? false, checking: r?.checking ?? false, waitS: r?.wait_s ?? 0 };
}

export async function apiLoginTTYStart(base: string, type: string, name: string): Promise<LoginTTYSession | null> {
  const r = await post<{ session?: WireLoginSession }>(`${ttyPath(base, type, name)}/start`);
  return mapSession(r?.session);
}

/* apiLoginTTYExtend returns the new ttl, or null when the cap is
   reached / no session is running (409). */
export async function apiLoginTTYExtend(
  base: string,
  type: string,
  name: string,
): Promise<{ remainingS: number; capS: number } | null> {
  try {
    const r = await post<{ remaining_s?: number; cap_s?: number }>(`${ttyPath(base, type, name)}/extend`);
    return { remainingS: r?.remaining_s ?? 0, capS: r?.cap_s ?? 0 };
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) return null;
    throw e;
  }
}

export async function apiLoginTTYKill(base: string, type: string, name: string): Promise<void> {
  try {
    await post<void>(`${ttyPath(base, type, name)}/kill`);
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) return; // already gone
    throw e;
  }
}

/* loginTTYWSURL builds the websocket URL for the terminal stream from
   the current page origin. */
export function loginTTYWSURL(base: string, type: string, name: string): string {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  return `${proto}://${location.host}${ttyPath(base, type, name)}/ws`;
}

/* TermFeedState is everything the terminal modal derives from the
   frame stream (besides raw output, which goes straight to xterm). */
export type TermFeedState = {
  links: string[]; // newest first
  success: { line: string; at: number } | null;
  failure: { line: string; at: number } | null;
  remainingS: number;
  capS: number;
  state: string;
  exitErr: string;
  account: LoginAccount | null;
};

/* applyFrame folds one websocket frame into the modal state. Pure —
   the ws handler stays a one-liner and this stays testable. */
export function applyFrame(st: TermFeedState, fr: LoginTTYFrame, now: number): TermFeedState {
  switch (fr.t) {
    case "link": {
      const url = fr.url ?? "";
      if (url === "" || st.links.includes(url)) return st;
      return { ...st, links: [url, ...st.links] };
    }
    case "success":
      return { ...st, success: { line: fr.line ?? "", at: now } };
    case "fail":
      return { ...st, failure: { line: fr.line ?? "", at: now } };
    case "ttl":
      return { ...st, remainingS: fr.remaining_s ?? st.remainingS, capS: fr.cap_s ?? st.capS };
    case "state":
      return {
        ...st,
        state: fr.state ?? st.state,
        exitErr: fr.exit_err ?? "",
        account: fr.account ? mapLoginAccount(fr.account) : st.account,
      };
    default:
      return st;
  }
}

/* fmtCountdown renders seconds as m:ss, clamped at 0:00. */
export function fmtCountdown(s: number): string {
  const v = Math.max(0, Math.floor(s));
  const m = Math.floor(v / 60);
  const ss = String(v % 60).padStart(2, "0");
  return `${m}:${ss}`;
}

/* usageLabel names the known claude rate-limit windows the way the
   CLI's Account & Usage screen does; unknown keys pass through so new
   API fields still render. */
export function usageLabel(key: string): string {
  switch (key) {
    case "five_hour":
      return "Session (5hr)";
    case "seven_day":
      return "Weekly (7 day)";
    case "seven_day_opus":
    case "seven_day_fable":
      return "Weekly Fable";
    default:
      return key;
  }
}

/* fmtResetsIn renders "how long until this window resets" as a single
   CLI-style unit: 25m / 3h / 4d. Empty for past, missing, or invalid
   timestamps. */
export function fmtResetsIn(resetsAt: string, nowMs: number): string {
  if (!resetsAt) return "";
  const t = Date.parse(resetsAt);
  if (isNaN(t) || t <= nowMs) return "";
  const mins = Math.ceil((t - nowMs) / 60_000);
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

/* prettyPlan renders the raw subscription type the way the CLI shows
   it ("Claude team", "Claude max"); non-claude plans pass through. */
export function prettyPlan(plan: string): string {
  switch (plan) {
    case "":
      return "";
    case "api-key":
      return "API key";
    case "free":
    case "pro":
    case "max":
    case "team":
    case "enterprise":
      return `Claude ${plan}`;
    default:
      return plan;
  }
}
