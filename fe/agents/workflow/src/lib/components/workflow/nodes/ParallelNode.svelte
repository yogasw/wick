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
  headBg="#a3e635"
  icon="∥"
  outputs={Math.max(1, (node.branches ?? []).length)}
>
  {#snippet body()}
    <div class="text-[10px] text-black-500">{(node.branches ?? []).length} branches</div>
  {/snippet}
</BaseNode>
