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
  headBg="#f43f5e"
  icon="⌥"
  outputs={2}
>
  {#snippet body()}
    <div class="font-mono text-[11px] text-black-700 dark:text-white-100-300 line-clamp-2">{node.expr ?? "—"}</div>
    <div class="mt-1 flex gap-1 text-[10px]">
      <span class="px-1.5 py-0.5 rounded bg-emerald-100 text-emerald-700">true</span>
      <span class="px-1.5 py-0.5 rounded bg-rose-100 text-rose-700">false</span>
    </div>
  {/snippet}
</BaseNode>
