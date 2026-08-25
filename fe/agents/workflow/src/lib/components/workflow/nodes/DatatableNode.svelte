<script lang="ts">
  // Shared component for every datatable_* variant. The `op` discriminator
  // lives in node.type — render the suffix as the badge title.
  import BaseNode from "./BaseNode.svelte";
  import type { Node } from "$lib/types/workflow";
  type Props = { node: Node; selected?: boolean; running?: boolean; errored?: boolean; onselect?: () => void };
  let { node, selected, running, errored, onselect }: Props = $props();
  const op = $derived(node.type.replace("datatable_", ""));
  const isBranchy = $derived(node.type === "datatable_get" || node.type === "datatable_exists");
  // Sub-label mirrors the legacy editor's one-line op summary.
  const sub = $derived.by(() => {
    switch (node.type) {
      case "datatable_get": return "load by id";
      case "datatable_exists": return "exists check";
      case "datatable_query": return "multi-row search";
      case "datatable_insert": return "insert row";
      case "datatable_upsert": return "upsert row";
      case "datatable_delete": return "delete row";
      case "datatable_count": return "count rows";
      default: return op;
    }
  });
</script>

<BaseNode
  id={node.id}
  type={node.type}
  label={node.label}
  {selected}
  {running}
  {errored}
  {onselect}
  headBg="#0ea5e9"
  icon="▤"
  outputs={isBranchy ? 2 : 1}
>
  {#snippet body()}
    <div class="text-[11px] text-black-700 dark:text-black-600 truncate">{sub}</div>
    {#if node.module}<div class="text-[10px] text-black-700 dark:text-black-600 truncate">{node.module}</div>{/if}
    {#if isBranchy}
      <div class="mt-1 flex gap-1 text-[10px]">
        {#if node.type === "datatable_exists"}
          <span class="px-1.5 py-0.5 rounded bg-emerald-100 text-emerald-700">true</span>
          <span class="px-1.5 py-0.5 rounded bg-rose-100 text-rose-700">false</span>
        {:else}
          <span class="px-1.5 py-0.5 rounded bg-emerald-100 text-emerald-700">found</span>
          <span class="px-1.5 py-0.5 rounded bg-rose-100 text-rose-700">not_found</span>
        {/if}
      </div>
    {/if}
  {/snippet}
</BaseNode>
