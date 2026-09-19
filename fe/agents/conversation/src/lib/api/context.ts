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
};

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
  /** Recent per-turn context levels, oldest first. */
  trend?: number[];
  /** Whether /compact does anything on this session's provider. False
   *  for codex: `codex exec` has no slash commands, so the request
   *  would reach the model as plain text and be answered with a claim
   *  rather than a compaction. */
  can_compact?: boolean;
  /** One sentence explaining a false can_compact, written by the
   *  server so the UI keeps no second copy of the reason. */
  compact_note?: string;
};

export async function fetchSessionContext(
  base: string,
  sessionId: string,
): Promise<SessionContext> {
  const res = await fetch(`${base}/api/sessions/${encodeURIComponent(sessionId)}/context`, {
    headers: { Accept: "application/json" },
  });
  if (!res.ok) throw new Error(`context: ${res.status}`);
  return (await res.json()) as SessionContext;
}
