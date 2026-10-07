<script lang="ts">
  import BaseNode from "./BaseNode.svelte";
  import type { Node } from "$lib/types/workflow";
  type Props = { node: Node; selected?: boolean; running?: boolean; errored?: boolean; onselect?: () => void };
  let { node, selected, running, errored, onselect }: Props = $props();
  // A branch routes by the `case:` label on its outgoing edges (any number of
  // them, e.g. allow/default). BaseNode paints one labelled output port per
  // case, so the body only shows the expression.
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
  headBg="#f43f5e"
  icon="⌥"
>
  {#snippet body()}
    <div class="font-mono text-[11px] text-black-700 dark:text-slate-300 line-clamp-2">{node.expr ?? "—"}</div>
  {/snippet}
</BaseNode>
