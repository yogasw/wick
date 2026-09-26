<script lang="ts">
  /* Proportional bars: what a total is made of. The store-wide ingest split
     and a project's page-kind mix are the same drawing over different
     sources, so the rows arrive built (stats.ts). */
  import { meterWidth, type MeterRow } from "./stats.js";

  type Props = {
    rows: MeterRow[];
    // empty is what to say when there is nothing to break down. A bar chart
    // of no bars is indistinguishable from a chart that failed to load.
    empty?: string;
    testid?: string;
  };
  let { rows, empty = "Nothing to break down yet.", testid }: Props = $props();
</script>

{#if rows.length === 0}
  <p class="px-5 py-4 text-xs leading-relaxed text-black-700 dark:text-black-600" data-testid={testid}>{empty}</p>
{:else}
  <div class="space-y-3 px-5 py-4" data-testid={testid}>
    {#each rows as r (r.id)}
      <div class="min-w-0">
        <div class="flex flex-wrap items-baseline justify-between gap-2">
          <span class="text-xs font-medium text-black-900 dark:text-white-100">{r.label}</span>
          <span class="font-mono text-xs tabular-nums text-black-800 dark:text-black-600">{r.value}</span>
        </div>
        <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-white-300 dark:bg-navy-600">
          <div
            class={`h-full rounded-full ${r.bar ?? "bg-green-500 dark:bg-green-400"}`}
            style={`width:${meterWidth(r.share)}%`}
          ></div>
        </div>
        {#if r.note}
          <p class="mt-1 text-[0.6875rem] leading-tight text-black-700 dark:text-black-600">{r.note}</p>
        {/if}
      </div>
    {/each}
  </div>
{/if}
