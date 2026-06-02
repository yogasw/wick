<script lang="ts">
  import BaseNode from "./BaseNode.svelte";
  import type { Node } from "$lib/types/workflow";
  type Props = { node: Node; selected?: boolean; running?: boolean; errored?: boolean; onselect?: () => void };
  let { node, selected, running, errored, onselect }: Props = $props();
  const rules = $derived(node.rules ?? []);
</script>

<BaseNode
  id={node.id}
  type={node.type}
  label={node.label}
  {selected}
  {running}
  {errored}
  {onselect}
  headBg="#f43f5e"
  icon="⇆"
  outputs={Math.max(1, rules.length)}
>
  {#snippet body()}
    {#if rules.length === 0}
      <div class="italic text-black-500 dark:text-white-700">no rules</div>
    {:else}
      <ul class="space-y-0.5">
        {#each rules.slice(0, 3) as r}
          <li class="font-mono text-[10px] truncate">→ <b>{r.case}</b>: {r.when}</li>
        {/each}
        {#if rules.length > 3}
          <li class="text-[10px] text-black-500">…{rules.length - 3} more</li>
        {/if}
      </ul>
    {/if}
  {/snippet}
</BaseNode>
