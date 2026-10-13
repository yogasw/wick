/* contextSeries.ts — the points the Context window panel draws.
 *
 * The API hands over one trail per provider as parallel arrays; the
 * panel wants one list of points it can plot, hover and select. Kept out
 * of the component so the numbers (turn labels, running totals, the
 * selection summary) can be tested without a DOM. */

import { clock24 } from "@wick-fe/common-ui";
import type { ContextSeries, SessionContext } from "./api/context.js";

export type Metric = "context" | "usage";
export type Range = "last" | "all";
export type ProviderScope = "this" | "all";
export type Aggregate = "turn" | "total";

/** How many points "Last 40" shows; the server's default window. */
export const LAST_N = 40;

export type Point = {
  provider: string;
  /** Index into the provider palette. */
  color: number;
  level: number;
  prevLevel: number | null;
  /** Epoch ms; null when the server sent no time. */
  at: number | null;
  /** The provider's real turn number; null for a level-only point. */
  turn: number | null;
  /** One sequence across every provider, merged view only. */
  sturn: number | null;
  /** What this turn put on the wire and cost; null when not reported. */
  sent: number | null;
  cost: number | null;
  /** Tokens this provider had spent by this point. */
  cum: number | null;
  /** Dollars this provider had spent by this point. */
  cumCost: number | null;
};

/** Provider colours, in the order providers appear (active first). */
export const PALETTE = [
  { text: "text-link-400", bg: "bg-link-400" },
  { text: "text-green-500", bg: "bg-green-500" },
  { text: "text-amber-500", bg: "bg-amber-500" },
  { text: "text-red-500", bg: "bg-red-500" },
];
export const paletteOf = (i: number) => PALETTE[i % PALETTE.length];

/** One provider's trail as points. Missing trend_turn (an older server)
 *  numbers every point back from the provider's turn total. */
export function pointsOf(
  s: ContextSeries,
  provider: string,
  color: number,
  turns: number,
  costTotal: number,
): Point[] {
  const trend = s.trend ?? [];
  const n = trend.length;
  const spent = s.trend_spent ?? [];
  const out: Point[] = [];
  for (let i = 0; i < n; i++) {
    const at = s.trend_at?.[i] ? Date.parse(s.trend_at[i]) : NaN;
    let turn: number | null;
    let sent: number | null;
    if (s.trend_turn) {
      turn = s.trend_turn[i] ?? null;
      sent = turn === null ? null : (s.trend_sent?.[i] ?? null);
    } else {
      turn = turns - (n - 1 - i) > 0 ? turns - (n - 1 - i) : null;
      sent = i > 0 && spent.length === n ? spent[i] - spent[i - 1] : null;
    }
    out.push({
      provider,
      color,
      level: trend[i] ?? 0,
      prevLevel: i > 0 ? (trend[i - 1] ?? 0) : null,
      at: Number.isFinite(at) ? at : null,
      turn,
      sturn: null,
      sent,
      cost: turn === null ? null : (s.trend_cost?.[i] ?? null),
      cum: spent.length === n ? spent[i] : null,
      cumCost: null,
    });
  }
  // Dollars by each point, walked back from the provider's exact total —
  // the same anchoring the server uses for trend_spent.
  if (s.trend_cost && costTotal > 0) {
    let after = 0;
    for (let i = n - 1; i >= 0; i--) {
      out[i].cumCost = Math.max(0, costTotal - after);
      after += out[i].cost ?? 0;
    }
  }
  return out;
}

/** The points to draw: the active provider's, or every provider merged
 *  in time order (Usage · All providers). Last 40 keeps the newest 40. */
export function pointsFor(data: SessionContext, merged: boolean, range: Range): Point[] {
  const rows = data.providers ?? [];
  const activeRow = rows.find((p) => p.provider === data.provider);
  const active = pointsOf(
    data,
    data.provider ?? "",
    0,
    activeRow?.turns ?? data.turns,
    activeRow?.totals.cost_usd ?? 0,
  );
  let list = active;
  if (merged) {
    let color = 0;
    const all = [active];
    for (const p of rows) {
      if (p.provider === data.provider) continue;
      color++;
      if (p.trend?.length) all.push(pointsOf(p, p.provider, color, p.turns, p.totals.cost_usd));
    }
    // Stable sort: points without a time keep their provider's order.
    list = all.flat().map((q, k) => ({ q, k })).sort((x, y) => {
      if (x.q.at === null || y.q.at === null) return x.k - y.k;
      return x.q.at - y.q.at || x.k - y.k;
    }).map((x) => x.q);
  }
  if (range === "last") list = list.slice(-LAST_N);
  if (merged) numberSession(list, rows.reduce((a, p) => a + p.turns, 0) || data.turns);
  return list;
}

/* Session turns count back from the session's turn total, like the
   server numbers a provider's: the newest turn point is the last turn. */
function numberSession(list: Point[], total: number) {
  let k = total;
  for (let i = list.length - 1; i >= 0; i--) {
    if (list[i].turn === null) continue;
    list[i].sturn = k > 0 ? k : null;
    k--;
  }
}

/** Providers present in a merged list, palette order. */
export function legendOf(list: Point[]): { provider: string; color: number }[] {
  const seen = new Map<string, number>();
  for (const q of list) if (!seen.has(q.provider)) seen.set(q.provider, q.color);
  return [...seen].map(([provider, color]) => ({ provider, color })).sort((a, b) => a.color - b.color);
}

/** The plotted value of each point. Total is a running sum from the
 *  start of the range, so its slope is the burn rate. */
export function valuesOf(list: Point[], metric: Metric, agg: Aggregate): number[] {
  if (metric === "context") return list.map((q) => q.level);
  if (agg === "turn") return list.map((q) => q.sent ?? 0);
  let run = 0;
  return list.map((q) => (run += q.sent ?? 0));
}

/** Running dollars from the start of the range, same order. */
export function runningCost(list: Point[]): number[] {
  let run = 0;
  return list.map((q) => (run += q.cost ?? 0));
}

/* ---- formatting ---- */

/** Compact token count for readouts: 397k, 1.2M, 2.04B. */
export function brief(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0";
  if (n < 1000) return String(Math.round(n));
  if (n < 1e6) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`;
  if (n < 1e9) return `${(n / 1e6).toFixed(n < 1e8 ? 1 : 0)}M`;
  return `${(n / 1e9).toFixed(2)}B`;
}
export function usd(n: number): string {
  return `$${(Number.isFinite(n) ? n : 0).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}
export function signed(n: number): string {
  return `${n > 0 ? "+" : n < 0 ? "−" : ""}${brief(Math.abs(n))}`;
}
const MON = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
export function dayOf(ms: number): string {
  const d = new Date(ms);
  return `${MON[d.getMonth()]} ${d.getDate()}`;
}
/** HH:MM, with the date in front when it is not the newest point's day. */
export function when(ms: number | null, newest: number | null): string {
  if (ms === null) return "";
  const hm = clock24(ms);
  return newest !== null && dayOf(ms) !== dayOf(newest) ? `${dayOf(ms)} ${hm}` : hm;
}
export const turnText = (q: Point) => (q.turn === null ? "—" : String(q.turn));

/* ---- readout ---- */

/** A piece of a readout line; the component styles it. */
export type Seg = {
  text: string;
  strong?: boolean;
  tone?: "up" | "down";
  /** A provider colour dot before the text. */
  dot?: number;
  /** Tooltip, shown as an ⓘ after the text. */
  info?: string;
};
export const INFO = "Every model call re-reads the context; most of it comes from cache.";

export type View = {
  list: Point[];
  vals: number[];
  metric: Metric;
  agg: Aggregate;
  merged: boolean;
  window: number;
};

/** The two readout lines for point i. */
export function readout(v: View, i: number): [Seg[], Seg[]] {
  const q = v.list[i];
  const newest = v.list[v.list.length - 1]?.at ?? null;
  const time = when(q.at, newest);
  const turnLbl: Seg[] = [{ text: q.turn === null ? "turn —" : `turn ${q.turn}`, strong: true }];
  if (v.merged) turnLbl.push({ text: ` (session ${q.sturn ?? "—"})` });
  const who: Seg[] = v.merged ? [{ text: q.provider, strong: true, dot: q.color }, { text: " · " }] : [];
  const tail = (s: string) => (s ? [{ text: ` · ${s}` }] : []);
  if (v.metric === "context") {
    const d = q.prevLevel === null ? null : q.level - q.prevLevel;
    const l1: Seg[] = [...turnLbl, { text: ` · ${brief(q.level)}` }];
    if (d !== null) l1.push({ text: " " }, { text: signed(d), tone: d > 0 ? "up" : d < 0 ? "down" : undefined });
    if (v.window > 0) l1.push({ text: ` · ${Math.round((q.level / v.window) * 100)}%` });
    l1.push(...tail(q.turn === null ? "no usage reported" : time));
    const l2: Seg[] = [];
    if (q.turn !== null) {
      if (q.sent !== null) l2.push({ text: `this turn ${brief(q.sent)} sent · ` });
      if (q.cum !== null) l2.push({ text: `${brief(q.cum)} spent so far`, info: INFO });
    }
    return [l1, l2];
  }
  if (v.agg === "total") {
    const cost = runningCost(v.list)[i];
    return [
      [...who, ...turnLbl, { text: ` · ${brief(v.vals[i])} in this range · ${usd(cost)}` }],
      q.cum === null ? (time ? [{ text: time }] : []) : [{ text: `${brief(q.cum)} spent so far` }, ...tail(time)],
    ];
  }
  const l1: Seg[] = q.turn === null || q.sent === null
    ? [...who, ...turnLbl, { text: " · no usage reported" }, ...tail(time)]
    : [...who, ...turnLbl, { text: ` · ${brief(q.sent)} sent${q.cost !== null ? ` · ${usd(q.cost)}` : ""}` }, ...tail(time)];
  const l2: Seg[] = [];
  if (q.cum !== null) {
    let t = `${brief(q.cum)} spent so far`;
    if (q.cumCost !== null) t += ` · ${usd(q.cumCost)}`;
    if (v.merged) t += ` on ${q.provider}`;
    l2.push({ text: t });
  }
  return [l1, l2];
}

/* ---- selection ---- */

export type Selection = { a: number; b: number };

/** Index of the highest point in the selection: sent in Usage, level in
 *  Context. */
export function peakOf(v: View, sel: Selection): number {
  let peak = sel.a;
  const val = (q: Point) => (v.metric === "usage" ? (q.sent ?? 0) : q.level);
  for (let i = sel.a; i <= sel.b; i++) if (val(v.list[i]) > val(v.list[peak])) peak = i;
  return peak;
}

/** The summary row shown while a range is selected. */
export function selectionSummary(v: View, sel: Selection): Seg[] {
  const part = v.list.slice(sel.a, sel.b + 1);
  const pq = v.list[peakOf(v, sel)];
  const turns = part.filter((q) => q.turn !== null);
  let range: string;
  if (v.merged) {
    const st = part.filter((q) => q.sturn !== null);
    range = st.length ? `session turns ${st[0].sturn}–${st[st.length - 1].sturn}` : `${part.length} points`;
  } else {
    range = turns.length ? `turns ${turns[0].turn}–${turns[turns.length - 1].turn}` : `${part.length} points`;
  }
  if (v.metric === "usage") {
    const sent = part.reduce((a, q) => a + (q.sent ?? 0), 0);
    const cost = part.reduce((a, q) => a + (q.cost ?? 0), 0);
    return [
      { text: range, strong: true }, { text: " · " }, { text: brief(sent), strong: true }, { text: " sent · " },
      { text: usd(cost), strong: true },
      { text: ` · peak ${v.merged ? `${pq.provider} ` : ""}turn ${turnText(pq)} (${brief(pq.sent ?? 0)})` },
    ];
  }
  const pct = v.window > 0 ? ` (${Math.round((pq.level / v.window) * 100)}%)` : "";
  return [
    { text: range, strong: true }, { text: " · peak " }, { text: brief(pq.level), strong: true },
    { text: `${pct} at turn ${turnText(pq)}` },
  ];
}

/** The label next to the range control: "turns 364–403" or "3 providers". */
export function spanLabel(list: Point[], merged: boolean): string {
  if (merged) {
    const n = legendOf(list).length;
    return `${n} provider${n === 1 ? "" : "s"}`;
  }
  const t = list.filter((q) => q.turn !== null);
  return t.length ? `turns ${t[0].turn}–${t[t.length - 1].turn}` : "";
}

/** Tick values for the big chart's Y axis. */
export function niceTicks(lo: number, hi: number, count: number): number[] {
  const raw = (hi - lo) / count;
  const p = Math.pow(10, Math.floor(Math.log10(raw || 1)));
  const step = [1, 2, 5, 10].map((k) => k * p).find((s) => s >= raw) || p * 10;
  const out: number[] = [];
  for (let v = Math.ceil(lo / step) * step; v <= hi + 1e-9; v += step) out.push(v);
  return out;
}

/** Points to SVG coordinates, split into runs of one provider so a
 *  merged view colours each run; a run reaches the next point so the
 *  line stays continuous. */
export function runsOf(
  list: Point[],
  i0: number,
  i1: number,
  merged: boolean,
): { color: number; from: number; to: number }[] {
  if (i1 < i0) return [];
  if (!merged) return [{ color: -1, from: i0, to: i1 }];
  const out: { color: number; from: number; to: number }[] = [];
  let s = i0;
  for (let i = i0 + 1; i <= i1 + 1; i++) {
    if (i <= i1 && list[i].color === list[s].color) continue;
    out.push({ color: list[s].color, from: s, to: Math.min(i, i1) });
    s = i;
  }
  return out;
}
