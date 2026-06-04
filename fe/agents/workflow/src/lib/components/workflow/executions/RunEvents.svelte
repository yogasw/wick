<script lang="ts">
  // EVENTS timeline. Each event row is timestamp + kind pill + the
  // node id (resolved to label when known). Kind pill colour mirrors
  // the status palette: completed = emerald, failed = rose, started =
  // amber, anything else = slate.
  import { draftWorkflow } from "$lib/stores/editor";
  import { fmtTimeOnly } from "./runHelpers";

  type Ev = { ts?: string; event?: string; node?: string };
  type Props = { events: Ev[] };
  let { events }: Props = $props();

  function labelFor(id: string | undefined): string {
    if (!id) return "";
    const wf = $draftWorkflow;
    const node = wf?.graph?.nodes?.find((n) => n.id === id);
    if (node?.label && node.label !== id) return node.label;
    const trig = wf?.triggers?.find((t) => t.id === id);
    if (trig?.label) return trig.label;
    return id;
  }

  function pillClass(event: string | undefined): string {
    if (!event) return "bg-slate-500/15 text-black-500 dark:text-black-600";
    if (event.includes("completed")) return "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300";
    if (event.includes("failed")) return "bg-rose-500/15 text-rose-700 dark:text-rose-300";
    if (event.includes("started")) return "bg-amber-500/15 text-amber-700 dark:text-amber-300";
    return "bg-slate-500/15 text-black-500 dark:text-black-600";
  }
</script>

<section>
  <div class="text-[11px] font-semibold tracking-wider text-black-700 dark:text-black-600 mb-2 flex items-center gap-2">
    <span>EVENTS</span>
    <span class="text-black-700 dark:text-black-500">{events.length}</span>
  </div>
  {#if events.length === 0}
    <p class="text-xs text-black-700 dark:text-black-600 italic">No events recorded.</p>
  {:else}
    <ul class="divide-y divide-white-300 dark:divide-navy-600 dark:divide-white-300 dark:divide-navy-600 text-xs font-mono">
      {#each events as ev, i (i)}
        <li class="flex items-center gap-2 py-1.5">
          <span class="text-black-700 dark:text-black-600 tabular-nums shrink-0">{fmtTimeOnly(ev.ts)}</span>
          <span class={"px-1.5 py-0.5 rounded text-[10px] " + pillClass(ev.event)}>{ev.event}</span>
          {#if ev.node}<span class="text-black-700 dark:text-black-500 truncate flex-1">{labelFor(ev.node)}</span>{/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>
