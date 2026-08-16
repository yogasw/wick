<script lang="ts">
  // A small time-series chart drawn as inline SVG.
  //
  // Inline SVG rather than a charting library: the whole page ships one
  // series type (a value over time), and a dependency would add build
  // weight and a theme-integration problem for something that is forty
  // lines of path arithmetic.

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
    // format renders the current value for the corner readout.
    format?: (v: number) => string;
  }

  let {
    points,
    max,
    label = "",
    color = "#3b82f6",
    height = 48,
    format = (v: number) => String(Math.round(v)),
  }: Props = $props();

  const W = 240;

  // A flat series still needs a visible line, so an all-zero (or all-equal)
  // set falls back to a scale of 1 rather than dividing by zero.
  const ceiling = $derived.by(() => {
    if (max !== undefined && max > 0) return max;
    const peak = points.length ? Math.max(...points) : 0;
    return peak > 0 ? peak : 1;
  });

  const line = $derived.by(() => {
    if (points.length === 0) return "";
    if (points.length === 1) {
      const y = height - (points[0] / ceiling) * height;
      return `M 0 ${y} L ${W} ${y}`;
    }
    const step = W / (points.length - 1);
    return points
      .map((v, i) => {
        const y = height - Math.min(1, v / ceiling) * height;
        return `${i === 0 ? "M" : "L"} ${(i * step).toFixed(1)} ${y.toFixed(1)}`;
      })
      .join(" ");
  });

  // The filled area is the same path closed along the baseline; it makes a
  // low-amplitude series legible at this height.
  const area = $derived(line === "" ? "" : `${line} L ${W} ${height} L 0 ${height} Z`);

  const current = $derived(points.length ? points[points.length - 1] : 0);
  const peak = $derived(points.length ? Math.max(...points) : 0);
  const gradientId = $derived(`spark-${label.replace(/[^a-z0-9]/gi, "")}-${color.replace("#", "")}`);
</script>

<div class="space-y-1">
  {#if label}
    <div class="flex items-baseline justify-between">
      <span class="text-xs font-medium text-black-700 dark:text-black-600">{label}</span>
      <span class="text-xs tabular-nums text-black-900 dark:text-white-100">
        {format(current)}
        {#if peak > current}
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
    <svg
      viewBox="0 0 {W} {height}"
      preserveAspectRatio="none"
      class="w-full"
      style="height: {height}px"
      role="img"
      aria-label={label ? `${label}: ${format(current)}` : "usage chart"}
    >
      <defs>
        <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color={color} stop-opacity="0.35" />
          <stop offset="100%" stop-color={color} stop-opacity="0.02" />
        </linearGradient>
      </defs>
      <path d={area} fill="url(#{gradientId})" />
      <path d={line} fill="none" stroke={color} stroke-width="1.5" vector-effect="non-scaling-stroke" />
    </svg>
  {/if}
</div>
