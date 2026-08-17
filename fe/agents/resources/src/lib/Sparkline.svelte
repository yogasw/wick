<script lang="ts">
  // A small time-series chart drawn as inline SVG, with a hover readout.
  //
  // Inline SVG rather than a charting library: the whole page ships one
  // series type (a value over time), and a dependency would add build
  // weight and a theme-integration problem for something that is a
  // hundred lines of path arithmetic.

  interface Props {
    // points are y-values in chronological order; x is implied by index,
    // which is correct here because samples arrive on a fixed interval.
    points: number[];
    // max pins the y-axis. Passing it explicitly lets several charts share
    // a scale (all agents against the machine total), which is what makes
    // them visually comparable.
    max?: number;
    label?: string;
    color?: string;
    height?: number;
    // format renders a value for the corner readout and the hover tooltip.
    format?: (v: number) => string;
    // times are ISO timestamps parallel to points, used to label the
    // hovered sample. Optional: without them the tooltip shows the value
    // alone rather than inventing a time.
    times?: string[];
  }

  let {
    points,
    max,
    label = "",
    color = "#3b82f6",
    height = 48,
    format = (v: number) => String(Math.round(v)),
    times = [],
  }: Props = $props();

  const W = 240;

  // A flat series still needs a visible line, so an all-zero (or all-equal)
  // set falls back to a scale of 1 rather than dividing by zero.
  const ceiling = $derived.by(() => {
    if (max !== undefined && max > 0) return max;
    const peak = points.length ? Math.max(...points) : 0;
    return peak > 0 ? peak : 1;
  });

  const step = $derived(points.length > 1 ? W / (points.length - 1) : 0);

  function yFor(v: number): number {
    return height - Math.min(1, v / ceiling) * height;
  }

  const line = $derived.by(() => {
    if (points.length === 0) return "";
    if (points.length === 1) {
      const y = yFor(points[0]);
      return `M 0 ${y} L ${W} ${y}`;
    }
    return points
      .map((v, i) => `${i === 0 ? "M" : "L"} ${(i * step).toFixed(1)} ${yFor(v).toFixed(1)}`)
      .join(" ");
  });

  // The filled area is the same path closed along the baseline; it makes a
  // low-amplitude series legible at this height.
  const area = $derived(line === "" ? "" : `${line} L ${W} ${height} L 0 ${height} Z`);

  const current = $derived(points.length ? points[points.length - 1] : 0);
  const peak = $derived(points.length ? Math.max(...points) : 0);
  const gradientId = $derived(`spark-${label.replace(/[^a-z0-9]/gi, "")}-${color.replace("#", "")}`);

  // Hover state. hoverIndex is the sample under the cursor, or null when
  // the pointer is away — reading a chart without being able to ask "what
  // is that spike" is most of the value of having one.
  let hoverIndex = $state<number | null>(null);
  let svgEl = $state<SVGSVGElement | null>(null);

  function onMove(e: PointerEvent): void {
    if (!svgEl || points.length === 0) return;
    const rect = svgEl.getBoundingClientRect();
    if (rect.width === 0) return;
    // The viewBox is scaled to the element width, so map through the ratio
    // rather than assuming 1:1 pixels.
    const ratio = (e.clientX - rect.left) / rect.width;
    const idx = Math.round(ratio * (points.length - 1));
    hoverIndex = Math.max(0, Math.min(points.length - 1, idx));
  }

  function onLeave(): void {
    hoverIndex = null;
  }

  const hoverX = $derived(hoverIndex === null ? 0 : hoverIndex * step);
  const hoverY = $derived(hoverIndex === null ? 0 : yFor(points[hoverIndex]));
  // Percentage position drives the HTML tooltip, which lives outside the
  // SVG so its text is not stretched by preserveAspectRatio="none".
  const hoverPct = $derived(
    points.length > 1 && hoverIndex !== null ? (hoverIndex / (points.length - 1)) * 100 : 0,
  );
  const hoverTime = $derived.by(() => {
    if (hoverIndex === null || !times[hoverIndex]) return "";
    const d = new Date(times[hoverIndex]);
    return Number.isNaN(d.getTime())
      ? ""
      : d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  });
</script>

<div class="space-y-1">
  {#if label}
    <div class="flex items-baseline justify-between">
      <span class="text-xs font-medium text-black-700 dark:text-black-600">{label}</span>
      <span class="text-xs tabular-nums text-black-900 dark:text-white-100">
        {format(hoverIndex === null ? current : points[hoverIndex])}
        {#if peak > current && hoverIndex === null}
          <span class="text-black-700 dark:text-black-600">· peak {format(peak)}</span>
        {/if}
      </span>
    </div>
  {/if}

  {#if points.length === 0}
    <div
      class="flex items-center justify-center rounded-lg border border-dashed border-white-300 text-xs text-black-700 dark:border-navy-600 dark:text-black-600"
      style="height: {height}px"
    >
      no samples yet
    </div>
  {:else}
    <div class="relative">
      <svg
        bind:this={svgEl}
        viewBox="0 0 {W} {height}"
        preserveAspectRatio="none"
        class="w-full touch-none"
        style="height: {height}px"
        role="img"
        aria-label={label ? `${label}: ${format(current)}` : "usage chart"}
        onpointermove={onMove}
        onpointerleave={onLeave}
      >
        <defs>
          <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color={color} stop-opacity="0.35" />
            <stop offset="100%" stop-color={color} stop-opacity="0.02" />
          </linearGradient>
        </defs>
        <path d={area} fill="url(#{gradientId})" />
        <path
          d={line}
          fill="none"
          stroke={color}
          stroke-width="1.5"
          vector-effect="non-scaling-stroke"
        />

        {#if hoverIndex !== null}
          <!-- Crosshair. vector-effect keeps it hairline-thin despite the
               non-uniform viewBox scaling. -->
          <line
            x1={hoverX}
            y1="0"
            x2={hoverX}
            y2={height}
            stroke="currentColor"
            stroke-width="1"
            stroke-dasharray="2 2"
            class="text-black-700 dark:text-black-600"
            vector-effect="non-scaling-stroke"
          />
          <circle
            cx={hoverX}
            cy={hoverY}
            r="3"
            fill={color}
            stroke="currentColor"
            stroke-width="1.5"
            class="text-white-100 dark:text-navy-700"
            vector-effect="non-scaling-stroke"
          />
        {/if}
      </svg>

      {#if hoverIndex !== null}
        <!-- Kept in HTML, not SVG: preserveAspectRatio="none" stretches the
             viewBox horizontally, which would distort any text inside it. -->
        <div
          class="pointer-events-none absolute -top-1 z-10 -translate-x-1/2 -translate-y-full whitespace-nowrap rounded-md border border-white-300 bg-white-100 px-2 py-1 text-xs shadow-sm dark:border-navy-600 dark:bg-navy-800"
          style="left: {hoverPct}%"
        >
          <span class="font-medium tabular-nums text-black-900 dark:text-white-100">
            {format(points[hoverIndex])}
          </span>
          {#if hoverTime}
            <span class="ml-1 text-black-700 dark:text-black-600">{hoverTime}</span>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</div>
