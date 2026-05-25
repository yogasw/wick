<script lang="ts">
  import type { RunSummary } from "$lib/api/workflow";
  type Props = { runs: RunSummary[]; onpick?: (runID: string) => void };
  let { runs, onpick }: Props = $props();
</script>

{#if runs.length === 0}
  <p class="text-xs text-black-500 dark:text-white-700">No runs yet.</p>
{:else}
  <ul class="divide-y divide-white-300 dark:divide-navy-600">
    {#each runs as r}
      <li class="flex items-center gap-3 py-1.5 text-xs">
        <span class="px-1.5 py-0.5 rounded text-[10px] uppercase"
              class:bg-emerald-100={r.status === "succeeded"}
              class:text-emerald-700={r.status === "succeeded"}
              class:bg-rose-100={r.status === "failed"}
              class:text-rose-700={r.status === "failed"}
              class:bg-amber-100={r.status === "running"}
              class:text-amber-700={r.status === "running"}
        >{r.status}</span>
        <button class="flex-1 text-left font-mono truncate hover:underline" onclick={() => onpick?.(r.run_id)}>{r.run_id}</button>
        <span class="text-black-500 dark:text-white-700">{r.started_at}</span>
      </li>
    {/each}
  </ul>
{/if}
