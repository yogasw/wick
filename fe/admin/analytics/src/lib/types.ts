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
  tokens?: AnalyticsToken[];
  daily?: AnalyticsPoint[];
};

export type AnalyticsChannel = {
  channel: string;
  sessions: number;
  users: number;
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
  users: AnalyticsUser[];
  channels: AnalyticsChannel[];
  projects: AnalyticsProject[];
  series: AnalyticsSeries;
  total_users: number;
  active_users_7d: number;
  sessions: number;
  unattributed_sessions: number;
};

/** What the streaming endpoint emits, one JSON object per line. */
export type AnalyticsStreamLine =
  | { type: "progress"; done: number; total: number }
  | { type: "result"; data: AnalyticsResponse }
  | { type: "error"; error: string };
