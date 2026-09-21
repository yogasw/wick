/* usageReport.ts — the token ledger read model.
 *
 * Kept out of api.ts on purpose: the ledger is a reporting surface with
 * its own refresh rhythm (a cached roll-up on the server, polled rarely),
 * and folding it into the provider CRUD module would tie a cheap page to
 * an expensive one. */

export type UsageTotals = {
  input: number;
  cache_read: number;
  cache_write: number;
  output: number;
  total: number;
  cost_usd: number;
  cache_hit_pct: number;
};

export type UsageSlice = {
  key: string;
  label?: string;
  totals: UsageTotals;
  sessions?: number;
  /** Percentage of the report total — the server computes it so every
   *  bar in the UI agrees on the denominator. */
  share: number;
};

/** One line of "where is this provider used". A bare session id named
 *  nothing — whose conversation it was, and under which project, is the
 *  actual question behind the list. */
export type SessionUse = {
  id: string;
  label?: string;
  project_id?: string;
  project_name?: string;
  user_id?: string;
  user_name?: string;
  last_at?: string;
  turns?: number;
  totals: UsageTotals;
};

/** A selectable range, sent by the server so the chips and the windows
 *  it understands cannot drift apart. */
export type WindowOption = { key: string; label: string };

/** Fields every ledger answer carries about the range it covers. */
type Windowed = {
  window: string;
  window_label: string;
  since?: string;
  windows: WindowOption[];
  /** True when a range could not be rebuilt in full for every session
   *  (the per-turn trail is capped). The figures are then a floor. */
  partial?: boolean;
  note?: string;
};

export type ProviderUsageDetail = Windowed & {
  provider: string;
  totals: UsageTotals;
  turns: number;
  /** Sessions that spent tokens on this provider, newest first. Can run
   *  to thousands, which is why the UI pages through it. */
  sessions: SessionUse[];
};

export type UsageReport = Windowed & {
  totals: UsageTotals;
  turns: number;
  sessions: number;
  by_provider: UsageSlice[];
  by_project: UsageSlice[];
  by_user: UsageSlice[];
};

export const EMPTY_TOTALS: UsageTotals = {
  input: 0,
  cache_read: 0,
  cache_write: 0,
  output: 0,
  total: 0,
  cost_usd: 0,
  cache_hit_pct: 0,
};

/** fetchUsageReport pulls the fleet-wide ledger. `refresh` bypasses the
 *  server's short cache — used by the explicit Refresh button, never by
 *  an automatic poll.
 *
 *  `endpoint` is the collection these two calls hang off, because the
 *  same ledger is served from two mounts: the agents tool
 *  ("/tools/agents/api/providers") and the admin analytics page
 *  ("/admin/analytics/ledger"). Passing it in is what lets ONE component
 *  render on both pages — which is the point, since the numbers must
 *  agree wherever they are read. */
export async function fetchUsageReport(
  endpoint: string,
  refresh = false,
  window = "all",
  since = "",
  until = "",
  scope: LedgerScope = {},
): Promise<UsageReport> {
  const url =
    `${endpoint}/usage?${rangeQuery(window, since, until)}${scopeQuery(scope)}` +
    (refresh ? "&refresh=1" : "");
  const res = await fetch(url, { headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error(`usage report: ${res.status}`);
  return (await res.json()) as UsageReport;
}

/** fetchProviderUsage answers "what did THIS provider cost, and where
 *  was it used". Same ledger, one slice of it. */
export async function fetchProviderUsage(
  endpoint: string,
  provider: string,
  refresh = false,
  window = "all",
  since = "",
  until = "",
  scope: LedgerScope = {},
): Promise<ProviderUsageDetail> {
  const url =
    `${endpoint}/${provider}/usage?${rangeQuery(window, since, until)}${scopeQuery(scope)}` +
    (refresh ? "&refresh=1" : "");
  const res = await fetch(url, { headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error(`provider usage: ${res.status}`);
  return (await res.json()) as ProviderUsageDetail;
}

/** LedgerScope narrows the ledger the way a page's filter bar does. A
 *  page that filters its chart by channel has to filter its cost by the
 *  same channel, or the two numbers below one filter disagree. */
export type LedgerScope = { channels?: string[]; instances?: string[] };

function scopeQuery(scope: LedgerScope): string {
  const parts: string[] = [];
  if (scope.channels?.length) parts.push(`channels=${encodeURIComponent(scope.channels.join(","))}`);
  if (scope.instances?.length)
    parts.push(`instances=${encodeURIComponent(scope.instances.join(","))}`);
  return parts.length ? `&${parts.join("&")}` : "";
}

/** rangeQuery builds the range half of the query. Explicit dates win over
 *  a named window — that is how a page with its own custom date picker
 *  asks for exactly the days it is showing. */
function rangeQuery(window: string, since: string, until: string): string {
  if (since || until) {
    const parts = [];
    if (since) parts.push(`since=${encodeURIComponent(since)}`);
    if (until) parts.push(`until=${encodeURIComponent(until)}`);
    return parts.join("&");
  }
  return `window=${encodeURIComponent(window)}`;
}

/** compactTokens renders 1_234_567 as "1.23M".
 *
 * Token counts run to eight digits and are read at a glance, not
 * audited — an exact number that nobody can compare is worse than a
 * rounded one they can. The full figure stays available in the title
 * attribute for anyone who does need it. */
export function compactTokens(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0";
  if (n < 1_000) return String(n);
  if (n < 1_000_000) return `${(n / 1_000).toFixed(n < 10_000 ? 1 : 0)}k`;
  if (n < 1_000_000_000) return `${(n / 1_000_000).toFixed(n < 10_000_000 ? 2 : 1)}M`;
  return `${(n / 1_000_000_000).toFixed(2)}B`;
}

/** formatCost keeps small amounts legible instead of rounding them to
 *  "$0.00", which reads as free. */
export function formatCost(usd: number): string {
  if (!Number.isFinite(usd) || usd <= 0) return "—";
  if (usd < 0.01) return "<$0.01";
  if (usd < 100) return `$${usd.toFixed(2)}`;
  return `$${Math.round(usd).toLocaleString()}`;
}

/** exact renders the full number with thousands separators, for titles. */
export function exact(n: number): string {
  return Number.isFinite(n) ? n.toLocaleString() : "0";
}

/** sinceText renders an ISO timestamp as "3h ago" — the ledger is read
 *  to find what is still running, and an absolute timestamp makes the
 *  reader do that subtraction in their head. */
export function sinceText(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "";
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  if (s < 86400 * 30) return `${Math.floor(s / 86400)}d ago`;
  return new Date(t).toLocaleDateString();
}
