<script lang="ts">
  import type { AnalyticsPoint } from "$lib/types";

  /** A second curve drawn over the total — one per channel, so "which door
   *  is busy" is read off the same axis instead of a second chart. */
  export type Overlay = { label: string; color: string; points: AnalyticsPoint[] };

  type Props = {
    points: AnalyticsPoint[];
    metric?: "sessions" | "logins" | "people";
    overlays?: Overlay[];
    height?: number;
  };
  let { points, metric = "sessions", overlays = [], height = 170 }: Props = $props();

  // Colours are INLINE, not Tailwind classes.
  //
  // The first version styled the paths with `fill-green-500/15` and
  // `stroke-green-500`. Those utilities exist in Tailwind but nothing else
  // on this page uses them, so the admin stylesheet never generated them —
  // the classes resolved to nothing and every path fell back to the SVG
  // default, which is solid black. The chart rendered as a black silhouette
  // in dark mode and a black blob in light mode.
  //
  // One green, chosen to read on both themes, plus alpha for the fill. An
  // inline attribute cannot be tree-shaken away by a CSS build that never
  // saw this file.
  const LINE = "#22c55e";
  const FILL = "rgba(34,197,94,0.16)";
  const AXIS = "rgba(148,163,184,0.35)";
  const GRID = "rgba(148,163,184,0.16)";

  const W = 720;
  const PAD = 8;

  const values = $derived(points.map((p) => p[metric] ?? 0));
  const overlayValues = $derived(overlays.map((o) => ({ ...o, values: o.points.map((p) => p[metric] ?? 0) })));
  // Every series shares one scale, otherwise a small channel would look as
  // big as the total and the comparison would be a lie.
  const peak = $derived(Math.max(1, ...values, ...overlayValues.flatMap((o) => o.values)));
  const total = $derived(values.reduce((a, b) => a + b, 0));
  const empty = $derived(total === 0);

  function x(i: number, n: number): number {
    if (n <= 1) return W / 2;
    return (i / (n - 1)) * W;
  }
  function y(v: number, h: number): number {
    return h - PAD - (v / peak) * (h - PAD * 2);
  }
  function line(vs: number[], h: number): string {
    return vs.map((v, i) => `${i === 0 ? "M" : "L"}${x(i, vs.length).toFixed(1)},${y(v, h).toFixed(1)}`).join(" ");
  }
  function area(vs: number[], h: number): string {
    if (vs.length === 0) return "";
    return `${line(vs, h)} L${W},${h - PAD} L0,${h - PAD} Z`;
  }

  // Hover: map pointer x to the nearest day. Cheap, and works on a chart
  // that has no DOM node per point.
  let hover = $state<number | null>(null);
  function onMove(e: MouseEvent) {
    const box = (e.currentTarget as SVGElement).getBoundingClientRect();
    if (box.width === 0 || values.length === 0) return;
    const frac = (e.clientX - box.left) / box.width;
    hover = Math.max(0, Math.min(values.length - 1, Math.round(frac * (values.length - 1))));
  }

  const shown = $derived(hover === null ? null : points[hover]);
  const unit = $derived(metric === "logins" ? "sign-ins" : metric === "people" ? "people" : "conversations");
</script>

<div class="relative">
  <svg
    viewBox={`0 0 ${W} ${height}`}
    preserveAspectRatio="none"
    role="img"
    aria-label={`${unit} per day`}
    class="w-full cursor-crosshair"
    style={`height:${height}px`}
    onmousemove={onMove}
    onmouseleave={() => (hover = null)}
  >
    <!-- Two faint gridlines at half and full peak, so a shape becomes a
         quantity without needing a labelled axis. -->
    {#each [0.5, 1] as frac}
      <line x1="0" y1={y(peak * frac, height)} x2={W} y2={y(peak * frac, height)} stroke={GRID} stroke-width="1" vector-effect="non-scaling-stroke" />
    {/each}
    <line x1="0" y1={height - PAD} x2={W} y2={height - PAD} stroke={AXIS} stroke-width="1" vector-effect="non-scaling-stroke" />

    {#if !empty}
      <path d={area(values, height)} fill={FILL} />
      <path
        d={line(values, height)}
        fill="none"
        stroke={LINE}
        stroke-width="2"
        stroke-linejoin="round"
        vector-effect="non-scaling-stroke"
      />
    {/if}

    {#each overlayValues as o}
      <path d={line(o.values, height)} fill="none" stroke={o.color} stroke-width="1.5" stroke-linejoin="round" vector-effect="non-scaling-stroke" />
    {/each}

    {#if hover !== null && !empty}
      <line
        x1={x(hover, values.length)}
        y1={PAD}
        x2={x(hover, values.length)}
        y2={height - PAD}
        stroke={AXIS}
        stroke-width="1"
        vector-effect="non-scaling-stroke"
      />
      <circle cx={x(hover, values.length)} cy={y(values[hover], height)} r="3.5" fill={LINE} />
    {/if}
  </svg>

  {#if empty}
    <!-- A flat line at zero is indistinguishable from a broken chart, so
         say which one it is. -->
    <p class="pointer-events-none absolute inset-0 flex items-center justify-center text-xs text-black-600 dark:text-black-700">
      nothing in this window
    </p>
  {/if}

  <!-- Readout. Pinned rather than following the cursor: a tooltip that
       moves is harder to read than one that stays where you expect it. -->
  <div class="mt-1 flex flex-wrap items-baseline gap-x-3 gap-y-1 text-[11px] text-black-700 dark:text-black-600">
    {#if shown}
      <span class="font-medium text-black-900 dark:text-white-100">{shown.date}</span>
      <span class="font-mono text-black-900 dark:text-white-100">{shown[metric] ?? 0}</span>
      <span>{unit}</span>
      {#each overlayValues as o}
        {#if o.values[hover ?? 0]}
          <span style={`color:${o.color}`}>{o.label} {o.values[hover ?? 0]}</span>
        {/if}
      {/each}
    {:else}
      <span>{points[0]?.date ?? "—"} → {points[points.length - 1]?.date ?? "—"}</span>
      <span class="font-mono text-black-900 dark:text-white-100">{total}</span>
      <span>{unit} · peak {peak}/day</span>
    {/if}
  </div>
</div>
