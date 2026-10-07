<script lang="ts">
  // Inputs of a node: every source that can start it — triggers, edges
  // (with the case they leave on), failure paths from nodes that fall
  // back here, and for a merge the inputs it still waits on with no wire.
  import type { Node } from "$lib/types/workflow";
  import { draftWorkflow, detailNodeID } from "$lib/stores/editor";
  import { incomingPorts, portTone, ERROR_KEY } from "./ports";

  type Props = { node: Node };
  let { node }: Props = $props();

  const ins = $derived(incomingPorts(node, $draftWorkflow));
  const hint = $derived(
    node.type === "merge"
      ? "A merge runs once every input below has finished."
      : "Any one of these finishing runs this node.",
  );
</script>

<div class="space-y-2" data-testid="inputs-panel">
  <div>
    <div class="text-xs font-medium">Inputs</div>
    <p class="text-[11px] text-black-700 dark:text-black-600">{hint}</p>
  </div>
  <div class="rounded border border-white-400 dark:border-navy-600 divide-y divide-white-400 dark:divide-navy-600">
    {#each ins as p (p.key)}
      <div class="flex items-center gap-2 px-2 py-1.5 text-[12px]" data-testid="input-row">
        {#if p.trigger}
          <span class="truncate font-mono">⚡ {p.label}</span>
        {:else}
          <button
            type="button"
            class="truncate font-mono text-emerald-600 hover:underline dark:text-emerald-400"
            onclick={() => detailNodeID.set(p.from)}
            title="Open {p.from}"
          >{p.label}</button>
        {/if}
        {#if p.detail}
          <span class="shrink-0 rounded px-1.5 font-mono text-[11px] {p.detail === 'error' ? portTone(ERROR_KEY) : p.trigger ? 'bg-slate-200 text-slate-700 dark:bg-slate-700 dark:text-slate-200' : portTone(p.detail)}">{p.detail}</span>
        {/if}
        <span class="text-black-700 dark:text-black-600">→ this node</span>
        {#if !p.connected}
          <span class="ml-auto text-[11px] italic text-rose-600 dark:text-rose-400">not wired — drag an edge from it</span>
        {/if}
      </div>
    {/each}
  </div>
</div>
