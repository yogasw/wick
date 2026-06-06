<script lang="ts">
  // NODES summary for a run — completed + failed lists. Click a node to
  // expand its full output (from state.json), so a "silent" agent run
  // shows what it actually produced without downloading JSON.
  import { draftWorkflow } from "$lib/stores/editor";

  type Props = {
    completed: string[];
    failed: string[];
    outputs?: Record<string, any>;
  };
  let { completed, failed, outputs = {} }: Props = $props();

  let open = $state<Set<string>>(new Set());
  function toggle(id: string) {
    const next = new Set(open);
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    open = next;
  }

  function hasOutput(id: string): boolean {
    const o = outputs[id];
    return o != null && (typeof o !== "object" || Object.keys(o).length > 0);
  }

  function labelFor(id: string): string {
    const wf = $draftWorkflow;
    const node = wf?.graph?.nodes?.find((n) => n.id === id);
    if (node?.label && node.label !== id) return node.label;
    const trig = wf?.triggers?.find((t) => t.id === id);
    if (trig?.label) return trig.label;
    if (trig?.type) return `${trig.type} trigger`;
    return id;
  }
</script>

<section>
  <div class="text-[11px] font-semibold tracking-wider text-black-700 dark:text-black-600 mb-2 flex items-center gap-2">
    <span>NODES</span>
    <span class="text-black-700 dark:text-black-500">{completed.length + failed.length} ran</span>
  </div>
  {#if completed.length === 0 && failed.length === 0}
    <p class="text-xs text-black-700 dark:text-black-600 italic">No nodes executed.</p>
  {:else}
    <ul class="divide-y divide-white-300 dark:divide-navy-600 text-xs">
      {#each [...completed.map((id) => ({ id, ok: true })), ...failed.map((id) => ({ id, ok: false }))] as n (n.id)}
        <li class="py-1.5">
          <button
            type="button"
            class={"w-full flex items-center gap-2 text-left " + (hasOutput(n.id) ? "cursor-pointer" : "cursor-default")}
            onclick={() => hasOutput(n.id) && toggle(n.id)}
          >
            <span class={"shrink-0 " + (n.ok ? "text-emerald-500" : "text-rose-500")}>{n.ok ? "✓" : "✗"}</span>
            <span class="truncate flex-1">{labelFor(n.id)}</span>
            {#if hasOutput(n.id)}<span class="text-black-700 dark:text-black-600 shrink-0">{open.has(n.id) ? "▾" : "▸"}</span>{/if}
          </button>
          {#if open.has(n.id) && hasOutput(n.id)}
            <pre class="mt-1.5 ml-4 overflow-auto rounded bg-white-200 dark:bg-navy-800 px-2 py-1.5 text-[10px] font-mono text-black-800 dark:text-white-100 whitespace-pre-wrap">{JSON.stringify(outputs[n.id], null, 2)}</pre>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>
