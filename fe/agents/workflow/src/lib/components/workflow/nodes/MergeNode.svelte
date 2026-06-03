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
  headBg="#475569"
  icon="⋈"
  inputs={Math.max(1, (node.inputs ?? []).length)}
>
  {#snippet body()}
    <div class="text-[10px] text-black-500">strategy: {node.strategy ?? "all"}</div>
  {/snippet}
</BaseNode>
