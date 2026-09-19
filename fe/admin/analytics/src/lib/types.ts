/** Shapes returned by GET /admin/analytics/users.json (internal/admin/analytics.go). */

/** One day of a curve. Days with nothing in them are still present, as
 *  zeros — a gap in a chart reads as "no data", which is a different claim
 *  from "nobody came". */
export type AnalyticsPoint = {
  date: string; // YYYY-MM-DD, UTC
  sessions: number;
  people?: number;
  logins?: number;
};

export type AnalyticsSeries = {
  days: number;
  from: string;
  points: AnalyticsPoint[];
  by_channel?: Record<string, AnalyticsPoint[]>;
  by_provider?: Record<string, AnalyticsPoint[]>;
};

/** The filter the server actually applied, echoed back so the page can
 *  label its numbers with the range they were computed over. */
export type AnalyticsWindow = {
  from: string;
  to: string;
  days: number;
  all?: boolean;
  channels?: string[];
  instances?: string[];
};

/** One provider instance — an account, in practice — and the models it was
 *  run with. Read from each session's agents.json. */
export type AnalyticsProvider = {
  key: string;
  type: string;
  instance?: string;
  sessions: number;
  users: number;
  last_active_at?: string;
  models?: AnalyticsKeyCount[];
};

/** A credential, as the page may show it: the record of a token, never the
 *  token. A revoked one stays listed because it still explains old traffic. */
export type AnalyticsToken = {
  id: string;
  name: string;
  masked: string;
  created_at?: string;
  last_used_at?: string;
  revoked?: boolean;
  sessions: number;
};

/** An id with the name a human recognises it by. */
export type AnalyticsRef = { id: string; name: string };

export type AnalyticsUser = {
  id: string;
  name: string;
  email: string;
  role: string;
  approved: boolean;
  avatar?: string;
  /** RFC3339, absent when this account has never signed in. */
  last_login_at?: string;
  logins: number;
  signed_in: boolean;
  /** Conversations they started, and ones they only took part in. */
  sessions: number;
  joined: number;
  last_active_at?: string;
  channels?: string[];
  projects?: AnalyticsRef[];
  agents?: string[];
  /** This person's own usage, busiest first: which account ran their work,
   *  and with which model. */
  providers?: AnalyticsKeyCount[];
  models?: AnalyticsKeyCount[];
  tokens?: AnalyticsToken[];
  daily?: AnalyticsPoint[];
  /** Their newest conversations, capped — the same list the project panel shows. */
  recent?: AnalyticsSessionRef[];
};

/** One configured bot on a channel — a Slack app somebody connected, a
 *  Telegram bot somebody registered. Key is what the filter uses. */
export type AnalyticsChannelInstance = {
  key: string;
  channel: string;
  owner_id?: string;
  owner_name?: string;
  owner_email?: string;
  sessions: number;
  users: number;
  unattributed?: number;
  last_active_at?: string;
};

export type AnalyticsChannel = {
  channel: string;
  sessions: number;
  users: number;
  instances?: AnalyticsChannelInstance[];
  unattributed?: number;
  last_active_at?: string;
};

export type AnalyticsKeyCount = { key: string; sessions: number };

export type AnalyticsProjectMember = {
  id: string;
  name: string;
  email?: string;
  sessions: number;
  last_active_at?: string;
};

export type AnalyticsSessionRef = {
  id: string;
  label?: string;
  channel: string;
  user?: string;
  token?: string;
  /** Which account ran it — several when the conversation switched mid-life. */
  providers?: string[];
  project_id?: string;
  project?: string;
  last_active_at?: string;
};

export type AnalyticsProject = {
  id: string;
  name: string;
  sessions: number;
  users: number;
  unattributed?: number;
  last_active_at?: string;
  members?: AnalyticsProjectMember[];
  channels?: AnalyticsKeyCount[];
  recent?: AnalyticsSessionRef[];
};

export type AnalyticsResponse = {
  generated_at: string;
  window: AnalyticsWindow;
  providers?: AnalyticsProvider[];
  users: AnalyticsUser[];
  channels: AnalyticsChannel[];
  projects: AnalyticsProject[];
  series: AnalyticsSeries;
  logins_recorded_since?: string;
  total_users: number;
  active_users_7d: number;
  /** Conversations inside the window, and on disk in total. */
  sessions: number;
  sessions_all_time: number;
  users_in_window: number;
  unattributed_sessions: number;
  /** Every channel ever seen, filtered or not — without it, filtering to
   *  one channel would remove the means of filtering back out. */
  known_channels?: string[];
};

/** What the streaming endpoint emits, one JSON object per line. */
export type AnalyticsStreamLine =
  | { type: "progress"; done: number; total: number }
  | { type: "result"; data: AnalyticsResponse }
  | { type: "error"; error: string };
