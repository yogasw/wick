<script lang="ts">
  import BaseNode from "./BaseNode.svelte";
  import type { Node } from "$lib/types/workflow";
  type Props = { node: Node; selected?: boolean; running?: boolean; errored?: boolean; onselect?: () => void };
  let { node, selected, running, errored, onselect }: Props = $props();
  const cases = $derived(node.output_cases ?? []);
</script>

<BaseNode
  id={node.id}
  type={node.type}
  label={node.label}
  {selected}
  {running}
  {errored}
  {onselect}
  headBg="#ec4899"
  icon="?"
  outputs={Math.max(1, cases.length)}
>
  {#snippet body()}
    <div class="text-[10px] text-black-500 dark:text-white-100-700 mb-1">prompt</div>
    <div class="line-clamp-2 text-xs">{node.prompt ?? "—"}</div>
    {#if cases.length}
      <div class="mt-2 flex flex-wrap gap-1">
        {#each cases as c}
          <span class="px-1.5 py-0.5 rounded bg-violet-100 dark:bg-violet-900/30 text-violet-700 dark:text-violet-300 text-[10px]">{c}</span>
        {/each}
      </div>
    {/if}
  {/snippet}
</BaseNode>
