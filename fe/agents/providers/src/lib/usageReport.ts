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

export type UsageReport = {
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
 *  an automatic poll. */
export async function fetchUsageReport(base: string, refresh = false): Promise<UsageReport> {
  const url = `${base}/api/providers/usage${refresh ? "?refresh=1" : ""}`;
  const res = await fetch(url, { headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error(`usage report: ${res.status}`);
  return (await res.json()) as UsageReport;
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
