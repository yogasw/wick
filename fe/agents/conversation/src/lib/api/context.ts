/* context.ts — the session's context-window reading.
 *
 * Served from the token ledger the store writes each turn, so opening
 * the panel costs nothing: no CLI call, no model call, no spawn. That is
 * why the composer can show the ring continuously. */

export type UsageTotals = {
  input: number;
  cache_read: number;
  cache_write: number;
  output: number;
  total: number;
  cost_usd: number;
  cache_hit_pct: number;
};

export type SessionContextProvider = {
  provider: string;
  model?: string;
  used: number;
  window: number;
  pct: number;
  turns: number;
  totals: UsageTotals;
  last_at?: string;
} & ContextSeries;

/** One provider's per-turn trail, oldest first; every array has one
 *  entry per point. A level-only point (a turn that ended without usage,
 *  a compaction) has null in trend_turn, trend_sent and trend_cost. On a
 *  provider row it is only present with ?providers=all, and never on the
 *  active row: the top level carries that one. */
export type ContextSeries = {
  /** Per-turn context levels. */
  trend?: number[];
  /** Cumulative tokens this provider had spent by each point. */
  trend_spent?: number[];
  /** When each point was recorded. Optional: an older server does not
   *  send it, and the curve is still readable without the clock. */
  trend_at?: string[];
  /** The provider's real turn number of each point. */
  trend_turn?: (number | null)[];
  /** What each turn itself sent, in tokens. */
  trend_sent?: (number | null)[];
  /** What each turn itself cost, in dollars. */
  trend_cost?: (number | null)[];
  /** Points stored, whatever was returned; the store caps the trail. */
  trend_total?: number;
};

/** What the panel asks for beyond the default: every stored point
 *  instead of the last 40, and the other providers' trails. */
export type ContextQuery = { trend?: "all"; providers?: "all" };

export type SessionContext = {
  session_id: string;
  /** Active provider — the one that answered most recently. Empty before
   *  the first finished turn. */
  provider?: string;
  model?: string;
  used: number;
  /** 0 when the provider never reported a window size; the UI must then
   *  show tokens instead of a percentage. */
  window: number;
  pct: number;
  turns: number;
  totals: UsageTotals;
  providers: SessionContextProvider[];
  /** Whether /compact does anything on this session's provider. False
   *  for codex: `codex exec` has no slash commands, so the request
   *  would reach the model as plain text and be answered with a claim
   *  rather than a compaction. */
  can_compact?: boolean;
  /** One sentence explaining a false can_compact, written by the
   *  server so the UI keeps no second copy of the reason. */
  compact_note?: string;
  /** Whether the active provider compacts by itself once the window
   *  fills, as it last reported (omp get_state, opencode config).
   *  Absent when the provider never said. */
  auto_compact?: boolean;
} & ContextSeries;

export async function fetchSessionContext(
  base: string,
  sessionId: string,
  query: ContextQuery = {},
): Promise<SessionContext> {
  const q = new URLSearchParams();
  if (query.trend) q.set("trend", query.trend);
  if (query.providers) q.set("providers", query.providers);
  const qs = q.toString() ? `?${q}` : "";
  const res = await fetch(`${base}/api/sessions/${encodeURIComponent(sessionId)}/context${qs}`, {
    headers: { Accept: "application/json" },
  });
  if (!res.ok) throw new Error(`context: ${res.status}`);
  return (await res.json()) as SessionContext;
}
