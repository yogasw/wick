<script lang="ts">
  /* The curve in the Context window panel and the controls around it.

     Context window plots how full the window was at each turn; Usage
     plots what each turn sent (Per turn) or the running total across
     the range (Total). Last 40 is what the polled endpoint already
     carries; All and All providers are fetched on demand, and while that
     runs the previous chart stays on screen, blurred, at the same height.

     Drag across the curve to select a range: a summary row appears for
     as long as the selection exists. Esc or a click outside clears it.
     The expand icon opens the same chart large, with zoom. */
  import type { Snippet } from "svelte";
  import type { ContextQuery, SessionContext } from "../api/context.js";
  import {
    type Aggregate, type Metric, type ProviderScope, type Range, type Selection, type Seg, type View,
    LAST_N, legendOf, paletteOf, peakOf, pointsFor, readout, runsOf, selectionSummary, spanLabel, valuesOf,
  } from "../contextSeries.js";
  import ContextChartModal from "./ContextChartModal.svelte";

  type Props = {
    data: SessionContext;
    /** Fetch the series with ?trend=all / ?providers=all. Absent, the
        panel draws whatever the polled reading carries. */
    loadSeries?: (q: ContextQuery) => Promise<SessionContext>;
  };
  let { data, loadSeries }: Props = $props();

  let metric = $state<Metric>("context");
  let range = $state<Range>("last");
  let scope = $state<ProviderScope>("this");
  let agg = $state<Aggregate>("turn");
  let aggOpen = $state(false);
  let hover = $state<number | null>(null);
  let sel = $state<Selection | null>(null);
  let zoomOpen = $state(false);

  const query = $derived<ContextQuery>({
    ...(range === "all" ? { trend: "all" as const } : {}),
    ...(metric === "usage" && scope === "all" ? { providers: "all" as const } : {}),
  });
  const queryKey = $derived(JSON.stringify(query));
  const needsFetch = $derived(!!loadSeries && queryKey !== "{}");

  /* The fetched series and the query it answers. A poll of the default
     reading refetches it quietly (same key, so no blur); only a new
     query blurs the chart while it loads. */
  let fetched = $state<{ key: string; data: SessionContext } | null>(null);
  /* The query whose last fetch failed. It ends the loading state — a
     chart blurred until the next poll looks frozen — and the chart keeps
     the series it last drew. */
  let failedKey = $state<string | null>(null);
  let lastReq = "";
  $effect(() => {
    if (!needsFetch || !loadSeries) return;
    const key = queryKey;
    const req = `${key}|${data.turns}|${data.trend_at?.[data.trend_at.length - 1] ?? ""}|${data.trend?.length ?? 0}`;
    if (req === lastReq) return;
    lastReq = req;
    loadSeries(query).then(
      (d) => {
        if (lastReq === req) {
          fetched = { key, data: d };
          if (failedKey === key) failedKey = null;
        }
      },
      () => {
        // Keep what is on screen; the next poll asks again.
        if (lastReq === req) {
          lastReq = "";
          failedKey = key;
        }
      },
    );
  });
  /* Waiting for (or failed to get) the series the controls ask for. */
  const pending = $derived(needsFetch && fetched?.key !== queryKey);
  const failed = $derived(pending && failedKey === queryKey);
  const loading = $derived(pending && !failed);
  const source = $derived(needsFetch ? (fetched?.data ?? data) : data);

  const merged = $derived(metric === "usage" && scope === "all");
  const live = $derived.by<View>(() => {
    const list = pointsFor(source, merged, range);
    return { list, vals: valuesOf(list, metric, agg), metric, agg, merged, window: source.window };
  });
  /* While a new series loads the chart keeps the last one it drew. */
  let held = $state<View | null>(null);
  $effect(() => {
    if (!pending) held = live;
  });
  const view = $derived(pending && held ? held : live);
  const n = $derived(view.list.length);

  /* The store keeps the last 500 points per provider; older ones are
     gone, and the label says so once the cap is reached. */
  const capHit = $derived((source.trend_total ?? 0) >= 500);
  const allLabel = $derived(capHit ? "All (last 500)" : "All");

  const validSel = $derived(sel && sel.b < n && sel.a !== sel.b ? sel : null);
  const peak = $derived(validSel ? peakOf(view, validSel) : null);
  const summary = $derived<Seg[]>(validSel ? selectionSummary(view, validSel) : []);
  const shown = $derived(hover !== null && hover < n ? hover : n > 0 ? n - 1 : null);
  const lines = $derived(shown === null ? null : readout(view, shown));
  const legend = $derived(merged ? legendOf(view.list) : []);

  /* small chart: viewBox 0 0 100 20, as before */
  const yMin = $derived(view.metric === "usage" && view.agg === "total" ? Math.min(...view.vals) : 0);
  const yMax = $derived(Math.max(...view.vals, yMin + 1));
  const tx = (i: number) => (n <= 1 ? 50 : (i / (n - 1)) * 100);
  const ty = (v: number) => 19 - ((v - yMin) / (yMax - yMin)) * 18;
  const pts = (from: number, to: number) => {
    const out: string[] = [];
    for (let i = from; i <= to; i++) out.push(`${tx(i).toFixed(2)},${ty(view.vals[i]).toFixed(2)}`);
    return out.join(" ");
  };
  const runs = $derived(runsOf(view.list, 0, n - 1, view.merged));

  function reset() {
    hover = null;
    sel = null;
  }
  function setMetric(v: Metric) {
    if (metric === v) return;
    metric = v;
    reset();
  }
  function setRange(v: Range) {
    if (range === v) return;
    range = v;
    reset();
  }
  function setScope(v: ProviderScope) {
    if (scope === v) return;
    scope = v;
    reset();
  }
  function setAgg(v: Aggregate) {
    agg = v;
    aggOpen = false;
    reset();
  }

  let box: HTMLDivElement | undefined = $state();
  let aggWrap: HTMLDivElement | undefined = $state();
  let modalEl: HTMLDivElement | undefined = $state();
  let drag: number | null = null;

  function idxAt(e: MouseEvent): number | null {
    if (!box || n === 0) return null;
    const rc = box.getBoundingClientRect();
    if (rc.width === 0) return null;
    return Math.round(Math.min(1, Math.max(0, (e.clientX - rc.left) / rc.width)) * (n - 1));
  }
  function onDown(e: MouseEvent) {
    if (loading) return;
    const i = idxAt(e);
    if (i === null) return;
    drag = i;
    sel = null;
    hover = i;
  }
  function onMove(e: MouseEvent) {
    if (loading) return;
    const i = idxAt(e);
    if (i === null) return;
    hover = i;
    if (drag !== null && i !== drag) sel = { a: Math.min(i, drag), b: Math.max(i, drag) };
  }
  function onLeave() {
    if (drag === null) hover = null;
  }

  /* Esc, as the panel sees it: true when this section used the key (the
     options popover or a selection), so the panel stays open. The modal
     handles its own Esc. */
  export function escape(): boolean {
    if (zoomOpen) return true;
    if (aggOpen) {
      aggOpen = false;
      return true;
    }
    if (sel) {
      sel = null;
      return true;
    }
    return false;
  }

  $effect(() => {
    function onDocDown(e: MouseEvent) {
      const t = e.target as Node;
      if (aggOpen && aggWrap && !aggWrap.contains(t)) aggOpen = false;
      if (sel && !zoomOpen && box && !box.contains(t) && !(modalEl && modalEl.contains(t))) {
        if (!(t instanceof Element && t.closest("[data-clear-sel]"))) sel = null;
      }
    }
    function onUp() {
      drag = null;
    }
    window.addEventListener("mousedown", onDocDown, true);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousedown", onDocDown, true);
      window.removeEventListener("mouseup", onUp);
    };
  });

  const ON = "rounded-md bg-white-200 px-2 py-0.5 font-medium text-black-900 dark:text-white-100 dark:bg-navy-800";
  const OFF = "rounded-md px-2 py-0.5 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800";
  const GROUP = "inline-flex items-center gap-0.5 rounded-lg border border-white-300 dark:border-navy-600 p-0.5";
</script>

{#snippet segs(list: Seg[])}
  {#each list as s, i (i)}
    {#if s.dot !== undefined}<span class="mr-1 inline-block h-2 w-2 rounded-sm {paletteOf(s.dot).bg}"></span>{/if}<span
      class={s.strong
        ? "font-medium text-black-900 dark:text-white-100"
        : s.tone === "up"
          ? "text-amber-600 dark:text-amber-400"
          : s.tone === "down"
            ? "text-green-600 dark:text-green-400"
            : ""}>{s.text}</span
    >{#if s.info}&nbsp;<span title={s.info} class="cursor-help">ⓘ</span>{/if}
  {/each}
{/snippet}

{#snippet controls()}
  <div class="flex items-center justify-between gap-2 text-[11px] tabular-nums">
    <div class="flex items-center gap-1.5">
      <div role="group" aria-label="Metric" class={GROUP}>
        <button type="button" aria-pressed={metric === "context"} class={metric === "context" ? ON : OFF} onclick={() => setMetric("context")}>Context window</button>
        <button type="button" aria-pressed={metric === "usage"} class={metric === "usage" ? ON : OFF} onclick={() => setMetric("usage")}>Usage</button>
      </div>
      <div role="group" aria-label="Range" class={GROUP}>
        <button type="button" aria-pressed={range === "last"} class={range === "last" ? ON : OFF} onclick={() => setRange("last")}>Last {LAST_N}</button>
        <button type="button" aria-pressed={range === "all"} class={range === "all" ? ON : OFF} onclick={() => setRange("all")}>{allLabel}</button>
      </div>
    </div>
    {#if !zoomOpen}
      <span class="inline-flex items-center">
        <span class="text-black-600 dark:text-black-700" data-testid="context-span">{spanLabel(view.list, view.merged)}</span>
        <button
          type="button"
          aria-label="Expand chart"
          title="Expand chart"
          data-testid="context-expand"
          class="ml-1.5 rounded px-1 text-xs leading-none text-black-700 dark:text-black-600 bg-white-100 dark:bg-navy-700 hover:bg-white-200 dark:hover:bg-navy-800"
          onclick={() => (zoomOpen = true)}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="block"><polyline points="15 3 21 3 21 9" /><polyline points="9 21 3 21 3 15" /><line x1="21" x2="14" y1="3" y2="10" /><line x1="3" x2="10" y1="21" y2="14" /></svg>
        </button>
      </span>
    {/if}
  </div>
  {#if metric === "usage"}
    <div class="mt-1.5 flex items-center justify-between gap-1.5 text-[11px]">
      <div role="group" aria-label="Providers" class={GROUP}>
        <button type="button" aria-pressed={scope === "this"} class={scope === "this" ? ON : OFF} onclick={() => setScope("this")}>This provider</button>
        <button type="button" aria-pressed={scope === "all"} class={scope === "all" ? ON : OFF} onclick={() => setScope("all")}>All providers</button>
      </div>
      <div class="relative" bind:this={aggWrap}>
        <button
          type="button"
          aria-label="Chart options"
          aria-haspopup="true"
          aria-expanded={aggOpen}
          title="Chart options"
          data-testid="context-options"
          class="rounded px-1 text-xs leading-none text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
          onclick={() => (aggOpen = !aggOpen)}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="block"><line x1="21" x2="14" y1="4" y2="4" /><line x1="10" x2="3" y1="4" y2="4" /><line x1="21" x2="12" y1="12" y2="12" /><line x1="8" x2="3" y1="12" y2="12" /><line x1="21" x2="16" y1="20" y2="20" /><line x1="12" x2="3" y1="20" y2="20" /><line x1="14" x2="14" y1="2" y2="6" /><line x1="8" x2="8" y1="10" y2="14" /><line x1="16" x2="16" y1="18" y2="22" /></svg>
        </button>
        {#if aggOpen}
          <div class="absolute right-0 top-full z-10 mt-1 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-1 shadow-lg">
            <div role="group" aria-label="Aggregate" class="flex flex-col gap-0.5 whitespace-nowrap">
              <button type="button" aria-pressed={agg === "turn"} class={agg === "turn" ? ON : OFF} onclick={() => setAgg("turn")}>Per turn</button>
              <button type="button" aria-pressed={agg === "total"} class={agg === "total" ? ON : OFF} onclick={() => setAgg("total")}>Total</button>
            </div>
          </div>
        {/if}
      </div>
    </div>
  {:else if (data.providers?.length ?? 0) > 1}
    <p class="mt-1 text-[11px] text-black-600 dark:text-black-700">
      Showing <span class="font-medium text-black-900 dark:text-white-100">{data.provider}</span> — the window belongs to one provider
    </p>
  {/if}
{/snippet}

{#snippet legendRow()}
  {#each legend as l (l.provider)}
    <span class="inline-flex items-center gap-1"><span class="inline-block h-2 w-2 rounded-sm {paletteOf(l.color).bg}"></span>{l.provider}</span>
  {/each}
{/snippet}

<div class="mt-3">
  {@render controls()}
  <div
    bind:this={box}
    class="ctx-chart relative mt-1 h-8 w-full"
    class:loading
    data-testid="context-chart"
    role="presentation"
    onmousedown={onDown}
    onmousemove={onMove}
    onmouseleave={onLeave}
  >
    {#if n >= 2}
      <svg
        viewBox="0 0 100 20"
        preserveAspectRatio="none"
        role="img"
        class="absolute inset-0 h-full w-full cursor-crosshair text-black-700 dark:text-black-600"
        aria-label={view.metric === "context" ? "Context level per turn" : view.agg === "total" ? "Running total of tokens sent" : "Tokens sent per turn"}
        data-testid="context-spark"
      >
        {#if validSel}
          <rect x={tx(validSel.a)} y="0" width={tx(validSel.b) - tx(validSel.a)} height="20" fill="currentColor" opacity="0.14" />
        {/if}
        {#each runs as r (r.from)}
          <g class={r.color >= 0 ? paletteOf(r.color).text : ""}>
            <polygon points="{tx(r.from).toFixed(2)},20 {pts(r.from, r.to)} {tx(r.to).toFixed(2)},20" fill="currentColor" opacity={r.color >= 0 ? 0.18 : 0.12} />
            <polyline points={pts(r.from, r.to)} fill="none" stroke="currentColor" stroke-width={r.color >= 0 ? 1.25 : 1} vector-effect="non-scaling-stroke" />
          </g>
        {/each}
        {#if peak !== null}
          <circle class="text-black-900 dark:text-white-100" cx={tx(peak)} cy={ty(view.vals[peak])} r="2.2" fill="currentColor" />
        {/if}
        {#if hover !== null && hover < n}
          <line x1={tx(hover)} y1="0" x2={tx(hover)} y2="20" stroke="currentColor" stroke-width="1" opacity="0.5" vector-effect="non-scaling-stroke" />
          <circle
            class={view.merged ? paletteOf(view.list[hover].color).text : ""}
            cx={tx(hover)}
            cy={ty(view.vals[hover])}
            r="2"
            fill="currentColor"
            opacity={view.list[hover].turn === null ? 0.45 : 1}
          />
        {/if}
      </svg>
    {/if}
    <div class="shim pointer-events-none text-link-400" aria-hidden="true"></div>
  </div>
  {#if failed}
    <p class="mt-1 text-[11px] text-red-600 dark:text-red-400" data-testid="context-load-error">
      Couldn't load this series — showing the last one.
    </p>
  {/if}
  {#if legend.length > 0}
    <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-black-700 dark:text-black-600">
      {@render legendRow()}
    </div>
  {/if}
  {#if validSel}
    <div class="mt-1 flex items-center gap-2 text-[11px] tabular-nums" data-testid="context-selection">
      <p class="min-w-0 flex-1 text-black-700 dark:text-black-600">
        {@render segs(summary)}<button
          type="button"
          data-clear-sel
          aria-label="Clear selection"
          title="Clear selection (Esc)"
          class="ml-1 rounded px-1 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
          onclick={() => (sel = null)}>×</button
        >
      </p>
    </div>
  {/if}
  <!-- Two lines tall, always, so the panel does not grow and shrink
       under the cursor as it crosses the curve. -->
  <p class="mt-1 min-h-[2.1rem] text-[11px] text-black-700 dark:text-black-600 tabular-nums" data-testid="context-spark-readout">
    {#if lines}
      {@render segs(lines[0])}
      <span class="block">{#if lines[1].length}{@render segs(lines[1])}{:else}&nbsp;{/if}</span>
    {/if}
  </p>
</div>

{#if zoomOpen}
  <ContextChartModal
    bind:el={modalEl}
    {view}
    {loading}
    bind:sel
    title={view.metric === "usage" ? (view.agg === "total" ? "Usage · running total" : "Usage · per turn") : "Context window"}
    {controls}
    {segs}
    legend={legend.length > 0 ? legendRow : undefined}
    onClose={() => (zoomOpen = false)}
  />
{/if}

<style>
  .ctx-chart {
    touch-action: none;
    user-select: none;
  }
  .ctx-chart .shim {
    position: absolute;
    left: 0;
    right: 0;
    top: 0;
    height: 2px;
    overflow: hidden;
    display: none;
    border-radius: 2px;
  }
  .ctx-chart .shim::after {
    content: "";
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    width: 40%;
    background: linear-gradient(90deg, transparent, currentColor, transparent);
    animation: ctx-shim 1s linear infinite;
  }
  .ctx-chart.loading .shim {
    display: block;
  }
  .ctx-chart.loading :global(svg) {
    filter: blur(2px);
    opacity: 0.55;
  }
  @keyframes ctx-shim {
    from {
      transform: translateX(-100%);
    }
    to {
      transform: translateX(250%);
    }
  }
</style>
