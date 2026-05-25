<script lang="ts">
  // Test cases list + run + result. The handler endpoint already exists
  // (`/workflows/edit/{id}/test-cases`); we render whatever the dual-mode
  // JSON variant returns.
  type Case = { name: string; assertions: number; last_result?: "pass" | "fail" };
  type Props = { cases: Case[]; running?: boolean; onRunAll?: () => void; onRunOne?: (name: string) => void };
  let { cases, running = false, onRunAll, onRunOne }: Props = $props();
</script>

<div class="flex items-center justify-between mb-2">
  <span class="text-xs">{cases.length} test case{cases.length === 1 ? "" : "s"}</span>
  <button class="px-2 py-1 rounded bg-emerald-500 text-white-100 text-xs" onclick={onRunAll} disabled={running}>
    {running ? "Running…" : "Run all"}
  </button>
</div>

{#if cases.length === 0}
  <p class="text-xs text-black-500 dark:text-white-700">No test cases yet. Capture one from a real run via the run detail panel.</p>
{:else}
  <ul class="divide-y divide-white-300 dark:divide-navy-600">
    {#each cases as c}
      <li class="flex items-center gap-3 py-1.5 text-xs">
        <span class="flex-1 truncate font-mono">{c.name}</span>
        <span class="text-black-500 dark:text-white-700">{c.assertions} assert</span>
        {#if c.last_result === "pass"}
          <span class="px-1.5 py-0.5 rounded bg-emerald-100 text-emerald-700">pass</span>
        {:else if c.last_result === "fail"}
          <span class="px-1.5 py-0.5 rounded bg-rose-100 text-rose-700">fail</span>
        {/if}
        <button class="text-emerald-600" onclick={() => onRunOne?.(c.name)}>run</button>
      </li>
    {/each}
  </ul>
{/if}
