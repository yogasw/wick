<script lang="ts">
  import BaseNode from "./BaseNode.svelte";
  import type { Node } from "$lib/types/workflow";
  import { draftWorkflow } from "$lib/stores/editor";
  type Props = { node: Node; selected?: boolean; running?: boolean; errored?: boolean; onselect?: () => void };
  let { node, selected, running, errored, onselect }: Props = $props();
  // A branch routes by the `case:` label on its outgoing edges (any number of
  // them, e.g. allow/default), not a fixed true/false pair. Show the labels the
  // graph really uses; only a branch with no labelled edge yet falls back to
  // the true/false a binary expression produces.
  const cases = $derived.by(() => {
    const seen: string[] = [];
    for (const e of $draftWorkflow?.graph?.edges ?? []) {
      if (e.from === node.id && e.case && !seen.includes(e.case)) seen.push(e.case);
    }
    return seen;
  });
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
  outputs={Math.max(1, cases.length)}
>
  {#snippet body()}
    <div class="font-mono text-[11px] text-black-700 dark:text-white-100-300 line-clamp-2">{node.expr ?? "—"}</div>
    <div class="mt-1 flex flex-wrap gap-1 text-[10px]">
      {#if cases.length === 0}
        <span class="px-1.5 py-0.5 rounded bg-emerald-100 text-emerald-700">true</span>
        <span class="px-1.5 py-0.5 rounded bg-rose-100 text-rose-700">false</span>
      {:else}
        {#each cases as c}
          <span
            class="px-1.5 py-0.5 rounded {c === 'default' ? 'bg-slate-200 text-slate-700' : 'bg-emerald-100 text-emerald-700'}"
          >{c}</span>
        {/each}
      {/if}
    </div>
  {/snippet}
</BaseNode>
