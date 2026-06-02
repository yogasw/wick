<script lang="ts">
  import { onMount } from "svelte";
  import { workflowAPI, type WorkflowSummary } from "$lib/api/workflow";

  type Props = { onpick?: (id: string) => void };
  let { onpick }: Props = $props();

  let items = $state<WorkflowSummary[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);

  onMount(async () => {
    try {
      const res = await workflowAPI.list();
      items = res.workflows ?? [];
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  });
</script>

<div class="h-screen flex flex-col">
  <header class="px-6 py-4 border-b border-white-300 dark:border-navy-600">
    <h1 class="text-lg font-semibold">Workflows</h1>
    <p class="text-xs text-black-500 dark:text-white-700">Pick a workflow to open the editor.</p>
  </header>

  <div class="flex-1 overflow-y-auto p-6">
    {#if loading}
      <p class="text-xs">Loading…</p>
    {:else if error}
      <p class="text-xs text-rose-600">{error}</p>
    {:else if items.length === 0}
      <p class="text-xs text-black-500 dark:text-white-700">No workflows yet.</p>
    {:else}
      <ul class="divide-y divide-white-300 dark:divide-navy-600 border border-white-300 dark:border-navy-600 rounded-lg overflow-hidden">
        {#each items as wf}
          <li class="flex items-center gap-3 px-4 py-2 hover:bg-white-200 dark:hover:bg-navy-700">
            <button class="flex-1 text-left" onclick={() => onpick?.(wf.id)}>
              <div class="text-sm font-medium">{wf.name}</div>
              <div class="text-[10px] font-mono text-black-500 dark:text-white-700">{wf.id}</div>
            </button>
            {#if wf.has_draft}
              <span class="px-1.5 py-0.5 rounded bg-amber-100 text-amber-800 text-[10px]">draft</span>
            {/if}
            <span class="px-1.5 py-0.5 rounded text-[10px]"
                  class:bg-emerald-100={wf.enabled}
                  class:text-emerald-800={wf.enabled}
                  class:bg-slate-200={!wf.enabled}
                  class:text-slate-700={!wf.enabled}
            >{wf.enabled ? "enabled" : "disabled"}</span>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>
