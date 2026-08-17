<script lang="ts">
  import { humanBytes, humanBps, humanPct } from "$lib/format.js";
  import type { TopProcessRow } from "$lib/types.js";

  interface Props {
    title: string;
    rows: TopProcessRow[];
    // metric decides which column is the headline number — the same
    // process can appear in all three tables for different reasons, and
    // the reason should be the thing in bold.
    metric: "memory" | "cpu" | "io";
    // barColor tints the inline proportion bar. Each table scales against
    // its OWN top row, so a table is readable even when its numbers are
    // small in absolute terms.
    barColor: string;
    emptyText?: string;
    // machineTotal turns an absolute figure into a share. Memory gets the
    // machine's RAM; CPU is already a percentage of one core and needs
    // none. Zero means unknown, and the share is then omitted rather than
    // rendered as a fabricated 0%.
    machineTotal?: number;
  }

  let {
    title,
    rows,
    metric,
    barColor,
    emptyText = "nothing measurable",
    machineTotal = 0,
  }: Props = $props();

  function value(r: TopProcessRow): number {
    if (metric === "memory") return r.rss_bytes;
    if (metric === "cpu") return r.cpu_pct;
    return r.io_read_bps + r.io_write_bps;
  }

  function label(r: TopProcessRow): string {
    if (metric === "memory") return humanBytes(r.rss_bytes);
    if (metric === "cpu") return humanPct(r.cpu_pct);
    return humanBps(r.io_read_bps + r.io_write_bps);
  }

  // The share of the machine, shown beside the absolute figure so memory
  // reads the way CPU already does. Empty when there is no denominator —
  // disk rates have no meaningful ceiling, and an unknown machine total
  // must not become a fabricated 0%.
  function share(r: TopProcessRow): string {
    if (metric !== "memory" || machineTotal <= 0) return "";
    return `${((r.rss_bytes / machineTotal) * 100).toFixed(1)}%`;
  }

  // Relative to the largest row in THIS table, not to a global scale: the
  // point is ranking within the metric, not comparing metrics.
  const peak = $derived(rows.length ? Math.max(...rows.map(value)) : 0);

  function widthPct(r: TopProcessRow): number {
    if (peak <= 0) return 0;
    return Math.max(2, (value(r) / peak) * 100);
  }
</script>

<div class="rounded-xl border border-white-300 bg-white-100 shadow-sm dark:border-navy-600 dark:bg-navy-700">
  <div class="border-b border-white-300 px-4 py-3 dark:border-navy-600">
    <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">{title}</h3>
  </div>

  {#if rows.length === 0}
    <p class="px-4 py-6 text-center text-xs text-black-700 dark:text-black-600">{emptyText}</p>
  {:else}
    <div class="divide-y divide-white-300 dark:divide-navy-600">
      {#each rows as r (r.name)}
        <div class="px-4 py-2">
          <div class="flex items-baseline justify-between gap-3">
            <span class="truncate text-xs text-black-900 dark:text-white-100" title={r.name}>
              {r.name}
              {#if (r.count ?? 0) > 1}
                <!-- Grouped: 26 renderers reported as one row, because
                     "chrome 662 MB" is the wrong answer when the real
                     figure is 3.9 GB across all of them. -->
                <span class="text-black-700 dark:text-black-600">× {r.count}</span>
              {/if}
            </span>
            <span class="shrink-0 text-xs tabular-nums">
              <span class="font-medium text-black-900 dark:text-white-100">{label(r)}</span>
              {#if share(r)}
                <span class="ml-1 text-black-700 dark:text-black-600">{share(r)}</span>
              {/if}
            </span>
          </div>
          <div class="mt-1 h-1 overflow-hidden rounded-full bg-white-300 dark:bg-navy-600">
            <div class="h-full rounded-full" style="width: {widthPct(r)}%; background-color: {barColor}"></div>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>
