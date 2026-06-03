<script lang="ts">
  import BaseNode from "./BaseNode.svelte";
  import type { Node } from "$lib/types/workflow";
  type Props = { node: Node; selected?: boolean; running?: boolean; errored?: boolean; onselect?: () => void };
  let { node, selected, running, errored, onselect }: Props = $props();
</script>

<BaseNode
  id={node.id}
  type={node.type}
  label={node.label}
  {selected}
  {running}
  {errored}
  {onselect}
  headBg="#8b5cf6"
  icon="A"
>
  {#snippet body()}
    <div class="text-[10px] text-black-500">provider: {node.provider ?? "—"}</div>
    {#if node.preset}<div class="text-[10px] text-black-500">preset: {node.preset}</div>{/if}
    {#if node.workspace}<div class="text-[10px] text-black-500">workspace: {node.workspace}</div>{/if}
  {/snippet}
</BaseNode>
