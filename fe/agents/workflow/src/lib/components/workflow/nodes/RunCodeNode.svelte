<script lang="ts">
  import BaseNode from "./BaseNode.svelte";
  import type { Node } from "$lib/types/workflow";
  type Props = { node: Node; selected?: boolean; running?: boolean; errored?: boolean; onselect?: () => void };
  let { node, selected, running, errored, onselect }: Props = $props();
  const lines = $derived((node.code ?? "").split("\n").length);
  const lang = $derived(node.lang === "js" ? "JavaScript" : "TypeScript");
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
  headBg="#475569"
  icon="TS"
>
  {#snippet body()}
    <div class="text-[10px] text-black-500">{lines} line{lines === 1 ? "" : "s"} of {lang}</div>
  {/snippet}
</BaseNode>
