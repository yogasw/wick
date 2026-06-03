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
  headBg="#84cc16"
  icon="ƒ"
>
  {#snippet body()}
    <div class="text-[10px] text-black-500">engine: {node.engine ?? "template"}</div>
    <pre class="font-mono text-[10px] line-clamp-2 whitespace-pre-wrap">{node.expression ?? "—"}</pre>
  {/snippet}
</BaseNode>
