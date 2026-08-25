<script lang="ts">
  import BaseNode from "./BaseNode.svelte";
  import type { Node } from "$lib/types/workflow";
  type Props = { node: Node; selected?: boolean; running?: boolean; errored?: boolean; onselect?: () => void };
  let { node, selected, running, errored, onselect }: Props = $props();
  const cmd = $derived((node.command ?? []).join(" "));
</script>

<BaseNode
  id={node.id}
  type={node.type}
  label={node.label}
  {selected}
  {running}
  {errored}
  {onselect}
  headBg="#64748b"
  icon="$_"
>
  {#snippet body()}
    <pre class="font-mono text-[10px] line-clamp-2 whitespace-pre-wrap">{cmd || "—"}</pre>
  {/snippet}
</BaseNode>
