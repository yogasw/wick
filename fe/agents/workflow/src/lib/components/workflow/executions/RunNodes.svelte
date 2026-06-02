<script lang="ts">
  // NODES summary for a run — completed + failed lists. Resolves
  // node ids to labels via the loaded workflow graph so the operator
  // sees `datatable_get_1` instead of a UUID. Falls back to the raw
  // id when no node row matches (deleted node, trigger, etc.).
  import { draftWorkflow } from "$lib/stores/editor";

  type Props = {
    completed: string[];
    failed: string[];
  };
  let { completed, failed }: Props = $props();

  function labelFor(id: string): string {
    const wf = $draftWorkflow;
    const node = wf?.graph?.nodes?.find((n) => n.id === id);
    if (node?.label && node.label !== id) return node.label;
    const trig = wf?.triggers?.find((t) => t.id === id);
    if (trig?.label) return trig.label;
    if (trig?.type) return `${trig.type} trigger`;
    return id;
  }
</script>

<section>
  <div class="text-[11px] font-semibold tracking-wider text-slate-500 mb-2 flex items-center gap-2">
    <span>NODES</span>
    <span class="text-slate-400">{completed.length + failed.length} ran</span>
  </div>
  {#if completed.length === 0 && failed.length === 0}
    <p class="text-xs text-slate-500 italic">No nodes executed.</p>
  {:else}
    <ul class="divide-y divide-slate-200 dark:divide-slate-700 text-xs">
      {#each completed as nodeID (nodeID)}
        <li class="flex items-center gap-2 py-1.5">
          <span class="text-emerald-500 shrink-0">✓</span>
          <span class="font-mono truncate flex-1">{labelFor(nodeID)}</span>
        </li>
      {/each}
      {#each failed as nodeID (nodeID)}
        <li class="flex items-center gap-2 py-1.5">
          <span class="text-rose-500 shrink-0">✗</span>
          <span class="font-mono truncate flex-1">{labelFor(nodeID)}</span>
        </li>
      {/each}
    </ul>
  {/if}
</section>
