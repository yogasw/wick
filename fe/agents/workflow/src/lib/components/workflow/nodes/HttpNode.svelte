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
  headBg="#f59e0b"
  icon="↗"
>
  {#snippet body()}
    <div class="font-mono text-[10px] flex items-center gap-1">
      <span class="px-1 rounded bg-sky-100 text-sky-800 uppercase">{node.method ?? "GET"}</span>
      <span class="truncate text-black-700 dark:text-white-300">{node.url ?? "—"}</span>
    </div>
  {/snippet}
</BaseNode>
