<script lang="ts">
  // EVENTS timeline. Each row is timestamp + kind pill + node; click a
  // row to expand its data (type, latency, output, error, tools_used).
  import { draftWorkflow } from "$lib/stores/editor";
  import { fmtTimeOnly } from "./runHelpers";

  type Ev = {
    ts?: string;
    event?: string;
    node?: string;
    case?: string;
    data?: Record<string, any>;
  };
  type Props = { events: Ev[] };
  let { events }: Props = $props();

  let open = $state<Set<number>>(new Set());
  function toggle(i: number) {
    const next = new Set(open);
    if (next.has(i)) {
      next.delete(i);
    } else {
      next.add(i);
    }
    open = next;
  }

  function labelFor(ev: Ev): string {
    const fromData = ev.data?.label;
    if (typeof fromData === "string" && fromData) return fromData;
    const id = ev.node;
    if (!id) return "";
    const wf = $draftWorkflow;
    const node = wf?.graph?.nodes?.find((n) => n.id === id);
    if (node?.label && node.label !== id) return node.label;
    const trig = wf?.triggers?.find((t) => t.id === id);
    if (trig?.label) return trig.label;
    return id;
  }

  function hasDetail(ev: Ev): boolean {
    return !!ev.data && Object.keys(ev.data).length > 0;
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
    <ul class="divide-y divide-white-300 dark:divide-navy-600 text-xs font-mono">
      {#each events as ev, i (i)}
        <li class="py-1.5">
          <button
            type="button"
            class={"w-full flex items-center gap-2 text-left " + (hasDetail(ev) ? "cursor-pointer" : "cursor-default")}
            onclick={() => hasDetail(ev) && toggle(i)}
          >
            <span class="text-black-700 dark:text-black-600 tabular-nums shrink-0">{fmtTimeOnly(ev.ts)}</span>
            <span class={"px-1.5 py-0.5 rounded text-[10px] " + pillClass(ev.event)}>{ev.event}</span>
            {#if ev.data?.type}<span class="text-black-700 dark:text-black-500 shrink-0">[{ev.data.type}]</span>{/if}
            <span class="text-black-700 dark:text-black-500 truncate flex-1">{labelFor(ev)}</span>
            {#if hasDetail(ev)}<span class="text-black-700 dark:text-black-600 shrink-0">{open.has(i) ? "▾" : "▸"}</span>{/if}
          </button>
          {#if open.has(i) && ev.data}
            <pre class="mt-1.5 ml-4 overflow-auto rounded bg-white-200 dark:bg-navy-800 px-2 py-1.5 text-[10px] text-black-800 dark:text-white-100 whitespace-pre-wrap">{JSON.stringify(ev.data, null, 2)}</pre>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>
