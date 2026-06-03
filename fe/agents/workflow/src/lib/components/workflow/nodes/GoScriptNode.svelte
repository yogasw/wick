<script lang="ts">
  import BaseNode from "./BaseNode.svelte";
  import type { Node } from "$lib/types/workflow";
  type Props = { node: Node; selected?: boolean; running?: boolean; errored?: boolean; onselect?: () => void };
  let { node, selected, running, errored, onselect }: Props = $props();
  const lines = $derived((node.code ?? "").split("\n").length);
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
  icon="Go"
>
  {#snippet body()}
    <div class="text-[10px] text-black-500">{lines} line{lines === 1 ? "" : "s"} of Go</div>
  {/snippet}
</BaseNode>
