<script lang="ts">
  import BaseNode from "./BaseNode.svelte";
  import type { Node } from "$lib/types/workflow";
  type Props = { node: Node; selected?: boolean; running?: boolean; errored?: boolean; onselect?: () => void };
  let { node, selected, running, errored, onselect }: Props = $props();
</script>

<BaseNode
  description={node.description ?? ""}
  id={node.id}
  type={node.type}
  label={node.label}
  {selected}
  {running}
  {errored}
  {onselect}
  headBg="#ec4899"
  icon="?"
>
  {#snippet body()}
    <div class="text-[10px] text-black-500 dark:text-white-100-700 mb-1">prompt</div>
    <div class="line-clamp-2 text-xs">{node.prompt ?? "—"}</div>
  {/snippet}
</BaseNode>
