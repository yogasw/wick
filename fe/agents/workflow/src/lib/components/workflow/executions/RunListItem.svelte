<script lang="ts">
  // Single row in the left runs list. Visual hierarchy: status pill +
  // short id on the first line, datetime + duration on the second.
  // Active row gets an emerald ring instead of a heavy bg so the
  // selection reads clearly on both light and dark.
  import type { RunSummary } from "$lib/api/workflow";
  import { fmtTimestamp, fmtDuration, statusBadgeClass, statusLabel, shortID, runKey, runKind, kindBadgeClass, kindLabel, triggerTag } from "./runHelpers";

  type Props = {
    run: RunSummary;
    active: boolean;
    onpick: (runID: string) => void;
  };
  let { run, active, onpick }: Props = $props();

  const kind = $derived(runKind(run));
  const tag = $derived(triggerTag(run));
</script>

<button
  type="button"
  class={`w-full text-left px-4 py-3 border-b border-slate-200 dark:border-navy-600 transition-colors ${active ? "bg-slate-100 dark:bg-navy-700 ring-1 ring-inset ring-emerald-500" : "hover:bg-slate-50 dark:hover:bg-navy-700/50"}`}
  onclick={() => onpick(runKey(run))}
>
  <div class="flex items-center gap-2 text-xs flex-wrap">
    <span class={"px-1.5 py-0.5 rounded text-[10px] uppercase tracking-wider " + statusBadgeClass(run.status)}>
      {statusLabel(run.status)}
    </span>
    <span
      class={"px-1.5 py-0.5 rounded text-[10px] uppercase tracking-wider " + kindBadgeClass(kind)}
      title={run.trigger_type ? `trigger: ${run.trigger_type}` : kindLabel(kind)}
    >
      {kindLabel(kind)}
    </span>
    {#if tag}
      <span class="px-1.5 py-0.5 rounded text-[10px] tracking-wider bg-amber-500/10 text-amber-700 dark:text-amber-300 border border-amber-400/30">
        {tag}
      </span>
    {/if}
    <span class="ml-auto text-black-700 dark:text-black-600 tabular-nums">{fmtDuration(run)}</span>
  </div>
  <div class="mt-1 flex items-center gap-2 text-[11px] text-black-700 dark:text-black-600">
    <span class="px-1.5 py-0.5 rounded bg-slate-200 dark:bg-navy-600 text-black-500 dark:text-black-600">{shortID(runKey(run))}</span>
    <span>{fmtTimestamp(run.started_at)}</span>
  </div>
</button>
