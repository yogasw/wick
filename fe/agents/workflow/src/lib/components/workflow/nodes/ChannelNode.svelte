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
  headBg="#6366f1"
  icon="#"
>
  {#snippet body()}
    <div class="text-[10px] text-black-500">channel: {node.channel ?? "—"}</div>
    <div class="text-[10px] text-black-500">op: {node.op ?? "—"}</div>
  {/snippet}
</BaseNode>
