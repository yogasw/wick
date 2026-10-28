<script lang="ts">
  /* The ledger over time: one bar per hour (short windows), day or month.
     It answers the question the totals above cannot — "is this going up
     or down" — and, scaled per conversation, "did my change make a
     conversation cheaper".

     One measure at a time, on purpose. Stacking fresh / cache read /
     cache write / output looks thorough, but cache reads are ~99% of the
     tokens here, so the other three would be slivers; and four hues in a
     stack do not stay apart for colour-blind readers. A picker keeps
     every measure on a scale where its own change is visible, and the
     readout below lists the whole breakdown for the hovered bar. */
  import { compactTokens, type TrendPoint } from "./usageReport.js";

  type Props = {
    points: TrendPoint[];
    /** "hour", "day" or "month" — the server picks it from the window. */
    unit?: string;
    height?: number;
  };
  let { points, unit = "day", height = 150 }: Props = $props();

  type Measure = "tokens" | "fresh" | "cache_read" | "cache_write" | "output" | "cost" | "turns" | "conversations";
  type Scale = "total" | "turn" | "conversation";

  const MEASURES: { id: Measure; label: string }[] = [
    { id: "tokens", label: "Tokens" },
    { id: "fresh", label: "Fresh input" },
    { id: "cache_read", label: "Cache read" },
    { id: "cache_write", label: "Cache write" },
    { id: "output", label: "Output" },
    { id: "cost", label: "Cost" },
    { id: "turns", label: "Turns" },
    { id: "conversations", label: "Conversations" },
  ];
  const SCALES: { id: Scale; label: string }[] = [
    { id: "total", label: "Total" },
    { id: "turn", label: "Per turn" },
    { id: "conversation", label: "Per conversation" },
  ];

  let measure = $state<Measure>("tokens");
  let scale = $state<Scale>("conversation");

  /* Turns per turn is always 1, conversations per anything is a count of
     itself: those pairs are not questions, so they are not offered. */
  function scaleAllowed(m: Measure, s: Scale): boolean {
    if (s === "total") return true;
    if (m === "conversations") return false;
    if (m === "turns") return s === "conversation";
    return true;
  }
  $effect(() => {
    if (!scaleAllowed(measure, scale)) scale = "total";
  });

  function raw(p: TrendPoint, m: Measure): number {
    switch (m) {
      case "fresh":
        return p.totals.input;
      case "cache_read":
        return p.totals.cache_read;
      case "cache_write":
        return p.totals.cache_write;
      case "output":
        return p.totals.output;
      case "cost":
        return p.totals.cost_usd;
      case "turns":
        return p.turns;
      case "conversations":
        return p.sessions;
      default:
        return p.totals.total;
    }
  }

  /** The bar's value, or null for a bucket with nothing to divide by — a
   *  quiet hour is "no data", not "zero tokens per conversation". */
  function value(p: TrendPoint): number | null {
    const v = raw(p, measure);
    if (scale === "turn") return p.turns > 0 ? v / p.turns : null;
    if (scale === "conversation") return p.sessions > 0 ? v / p.sessions : null;
    return v;
  }

  function fmt(v: number | null, m: Measure = measure): string {
    if (v === null) return "—";
    if (m === "cost") {
      if (v <= 0) return "$0";
      if (v < 0.01) return `$${v.toFixed(4)}`;
      if (v < 100) return `$${v.toFixed(2)}`;
      return `$${Math.round(v).toLocaleString()}`;
    }
    if (m === "turns" || m === "conversations") return Number.isInteger(v) ? String(v) : v.toFixed(1);
    return compactTokens(Math.round(v));
  }

  const values = $derived(points.map(value));
  const peak = $derived(Math.max(0, ...values.map((v) => v ?? 0)));
  const empty = $derived(peak === 0);

  /* "Did it go down": the mean of the buckets that had any use, first
     half against second half. Means rather than first-vs-last bar so one
     odd hour at either end cannot flip the verdict. */
  const change = $derived.by(() => {
    const used = values.filter((v): v is number => v !== null && (scale !== "total" || v > 0));
    if (used.length < 4) return null;
    const half = Math.floor(used.length / 2);
    const mean = (xs: number[]) => xs.reduce((a, b) => a + b, 0) / xs.length;
    const a = mean(used.slice(0, half));
    const b = mean(used.slice(used.length - half));
    if (a <= 0) return null;
    return { from: a, to: b, pct: ((b - a) / a) * 100 };
  });

  /* Over the whole range: the total for "Total", otherwise the ratio of
     the sums (not the mean of the ratios, which would weigh a one-turn
     hour the same as a busy one). */
  const overall = $derived.by(() => {
    const sum = points.reduce((a, p) => a + raw(p, measure), 0);
    if (scale === "turn") {
      const t = points.reduce((a, p) => a + p.turns, 0);
      return t > 0 ? sum / t : null;
    }
    if (scale === "conversation") {
      const c = points.reduce((a, p) => a + p.sessions, 0);
      return c > 0 ? sum / c : null;
    }
    return sum;
  });

  // Inline colours, as in the analytics chart: utility classes nobody
  // else on the page uses are not in the admin stylesheet.
  const BAR = "#22c55e";
  const BAR_HOVER = "#16a34a";
  const AXIS = "rgba(148,163,184,0.35)";
  const GRID = "rgba(148,163,184,0.16)";
  const W = 720;
  const PAD = 6;
  const GAP = 2;

  const slot = $derived(points.length > 0 ? W / points.length : W);
  const barW = $derived(Math.max(1, slot - (points.length > 60 ? 1 : GAP)));
  function y(v: number): number {
    return height - PAD - (peak > 0 ? (v / peak) * (height - PAD * 2) : 0);
  }

  let hover = $state<number | null>(null);
  function onMove(e: MouseEvent) {
    const box = (e.currentTarget as SVGElement).getBoundingClientRect();
    if (box.width === 0 || points.length === 0) return;
    const i = Math.floor(((e.clientX - box.left) / box.width) * points.length);
    hover = Math.max(0, Math.min(points.length - 1, i));
  }
  const shown = $derived(hover === null ? null : points[hover]);

  const measureLabel = $derived(MEASURES.find((m) => m.id === measure)?.label ?? "");
  const scaleLabel = $derived(scale === "turn" ? "per turn" : scale === "conversation" ? "per conversation" : "");
  const unitLabel = $derived(unit === "hour" ? "hour" : unit === "month" ? "month" : "day");
  /* Month buckets are folded from days, so a conversation that ran on
     three days of the month counts three times. Say so. */
  const convWord = $derived(unit === "month" ? "conversation-days" : "conversations");

  /** Axis ticks: first, middle and last bucket — enough to place a bar in
   *  time without a label colliding with its neighbour. */
  const ticks = $derived(
    points.length === 0
      ? []
      : [...new Set([0, Math.floor((points.length - 1) / 2), points.length - 1])].map((i) => ({
          i,
          label: unit === "hour" ? points[i].key.slice(11) : points[i].key,
        })),
  );
</script>

<div class="px-5 py-3 border-t border-white-300 dark:border-navy-600" data-testid="token-trend">
  <div class="mb-2 flex flex-wrap items-center gap-x-3 gap-y-2">
    <h3 class="text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">
      Over time <span class="normal-case">· per {unitLabel}</span>
    </h3>
    <div class="flex flex-wrap gap-1" role="group" aria-label="Measure">
      {#each MEASURES as m (m.id)}
        <button
          type="button"
          onclick={() => (measure = m.id)}
          aria-pressed={measure === m.id}
          class="rounded-lg border px-2 py-0.5 text-[11px] {measure === m.id
            ? 'border-green-400 text-green-700 dark:text-green-300'
            : 'border-white-300 text-black-700 dark:border-navy-600 dark:text-black-600'}"
        >
          {m.label}
        </button>
      {/each}
    </div>
    <div class="ml-auto flex overflow-hidden rounded-lg border border-white-300 dark:border-navy-600" role="group" aria-label="Scale">
      {#each SCALES as s (s.id)}
        <button
          type="button"
          disabled={!scaleAllowed(measure, s.id)}
          onclick={() => (scale = s.id)}
          aria-pressed={scale === s.id}
          class="px-2 py-0.5 text-[11px] disabled:opacity-40 {scale === s.id
            ? 'bg-green-500/10 text-green-700 dark:text-green-300'
            : 'text-black-700 dark:text-black-600'}"
        >
          {s.label}
        </button>
      {/each}
    </div>
  </div>

  <!-- The peak sits above the plot, not on it: a label inside the
       plot lands on the tallest bar. -->
  <div class="h-3 text-right text-[10px] text-black-600 dark:text-black-700">
    {#if !empty}peak {fmt(peak)}{/if}
  </div>
  <div class="relative">
    <svg
      viewBox={`0 0 ${W} ${height}`}
      preserveAspectRatio="none"
      role="img"
      aria-label={`${measureLabel} ${scaleLabel} per ${unitLabel}`}
      class="w-full cursor-crosshair"
      style={`height:${height}px`}
      onmousemove={onMove}
      onmouseleave={() => (hover = null)}
    >
      {#each [0.5, 1] as frac}
        <line x1="0" y1={y(peak * frac)} x2={W} y2={y(peak * frac)} stroke={GRID} stroke-width="1" vector-effect="non-scaling-stroke" />
      {/each}
      {#each values as v, i}
        {#if v !== null && v > 0}
          <rect
            x={i * slot + (slot - barW) / 2}
            y={y(v)}
            width={barW}
            height={Math.max(1, height - PAD - y(v))}
            fill={hover === i ? BAR_HOVER : BAR}
            opacity={hover === null || hover === i ? 1 : 0.55}
          />
        {/if}
      {/each}
      <line x1="0" y1={height - PAD} x2={W} y2={height - PAD} stroke={AXIS} stroke-width="1" vector-effect="non-scaling-stroke" />
    </svg>
    {#if empty}
      <p class="pointer-events-none absolute inset-0 flex items-center justify-center text-xs text-black-600 dark:text-black-700">
        nothing in this window
      </p>
    {/if}
  </div>
  <div class="relative mt-0.5 h-3 text-[10px] text-black-600 dark:text-black-700">
    {#each ticks as t (t.i)}
      <span
        class="absolute whitespace-nowrap"
        style={`left:${((t.i + 0.5) / points.length) * 100}%;transform:translateX(${t.i === 0 ? "0" : t.i === points.length - 1 ? "-100%" : "-50%"})`}
      >
        {t.label}
      </span>
    {/each}
  </div>

  <!-- Readout, pinned like the analytics chart's: the hovered bar's whole
       breakdown, or the range's summary and which way it went. -->
  <div class="mt-2 flex flex-wrap items-baseline gap-x-3 gap-y-1 text-[11px] text-black-700 dark:text-black-600" data-testid="token-trend-readout">
    {#if shown}
      <span class="font-medium text-black-900 dark:text-white-100">{shown.key}</span>
      <span class="font-mono text-black-900 dark:text-white-100">{fmt(value(shown))}</span>
      <span>{measureLabel.toLowerCase()} {scaleLabel}</span>
      <span>· {shown.sessions} {convWord}</span>
      <span>· {shown.turns} turns</span>
      <span>· fresh {compactTokens(shown.totals.input)}</span>
      <span>· cache read {compactTokens(shown.totals.cache_read)}</span>
      <span>· cache write {compactTokens(shown.totals.cache_write)}</span>
      <span>· out {compactTokens(shown.totals.output)}</span>
      <span>· {fmt(shown.totals.cost_usd, "cost")}</span>
      {#if shown.sessions > 0}
        <span>· {compactTokens(Math.round(shown.totals.total / shown.sessions))} tokens / conversation</span>
      {/if}
    {:else}
      <span>{points[0]?.key ?? "—"} → {points[points.length - 1]?.key ?? "—"}</span>
      <span class="font-mono text-black-900 dark:text-white-100">{fmt(overall)}</span>
      <span>{measureLabel.toLowerCase()} {scaleLabel || "in range"}</span>
      {#if change}
        <span data-testid="token-trend-change">
          · second half vs first:
          <span class="font-medium text-black-900 dark:text-white-100">
            {change.pct >= 0 ? "▲" : "▼"} {Math.abs(change.pct).toFixed(0)}%
          </span>
          ({fmt(change.from)} → {fmt(change.to)})
        </span>
      {/if}
    {/if}
  </div>
</div>
