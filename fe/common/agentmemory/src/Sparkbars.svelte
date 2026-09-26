<script lang="ts">
  /* One bar a day: the shape four static numbers cannot show — whether a
     memory is still being written to, or stopped a fortnight ago.

     An empty day is drawn as a floor rather than left out, so a gap is
     visible instead of missing. The series arrives built (writeTimeline in
     projectview.ts); this only draws it. */
  import type { WriteDay } from "./projectview.js";

  type Props = {
    days: WriteDay[];
    ariaLabel: string;
    // unit names what a bar counts, for the per-day hover.
    unit?: string;
    testid?: string;
  };
  let { days, ariaLabel, unit = "page write", testid }: Props = $props();

  const peak = $derived(Math.max(1, ...days.map((d) => d.count)));
</script>

<div class="flex h-16 items-end gap-0.5" role="img" aria-label={ariaLabel} data-testid={testid}>
  {#each days as d (d.date)}
    <div
      class={`flex-1 rounded-sm ${d.count > 0 ? "bg-green-500 dark:bg-green-400" : "bg-white-300 dark:bg-navy-600"}`}
      style={`height: ${d.count > 0 ? Math.max(8, Math.round((d.count / peak) * 100)) : 4}%`}
      title={`${d.date}: ${d.count} ${unit}${d.count === 1 ? "" : "s"}`}
    ></div>
  {/each}
</div>
<div class="mt-1 flex justify-between text-[0.6875rem] text-black-700 dark:text-black-600">
  <span>{days[0]?.date ?? ""}</span>
  <span>today</span>
</div>
