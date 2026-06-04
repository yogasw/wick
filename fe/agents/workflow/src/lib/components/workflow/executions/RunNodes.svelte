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
  <div class="text-[11px] font-semibold tracking-wider text-black-700 dark:text-black-600 mb-2 flex items-center gap-2">
    <span>NODES</span>
    <span class="text-black-700 dark:text-black-500">{completed.length + failed.length} ran</span>
  </div>
  {#if completed.length === 0 && failed.length === 0}
    <p class="text-xs text-black-700 dark:text-black-600 italic">No nodes executed.</p>
  {:else}
    <ul class="divide-y divide-white-300 dark:divide-navy-600 dark:divide-white-300 dark:divide-navy-600 text-xs">
      {#each completed as nodeID (nodeID)}
        <li class="flex items-center gap-2 py-1.5">
          <span class="text-emerald-500 shrink-0">✓</span>
          <span class="truncate flex-1">{labelFor(nodeID)}</span>
        </li>
      {/each}
      {#each failed as nodeID (nodeID)}
        <li class="flex items-center gap-2 py-1.5">
          <span class="text-rose-500 shrink-0">✗</span>
          <span class="truncate flex-1">{labelFor(nodeID)}</span>
        </li>
      {/each}
    </ul>
  {/if}
</section>
