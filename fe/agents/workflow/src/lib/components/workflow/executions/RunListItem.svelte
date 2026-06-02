<script lang="ts">
  // Single row in the left runs list. Visual hierarchy: status pill +
  // short id on the first line, datetime + duration on the second.
  // Active row gets an emerald ring instead of a heavy bg so the
  // selection reads clearly on both light and dark.
  import type { RunSummary } from "$lib/api/workflow";
  import { fmtTimestamp, fmtDuration, statusBadgeClass, statusLabel, shortID, runKey } from "./runHelpers";

  type Props = {
    run: RunSummary;
    active: boolean;
    onpick: (runID: string) => void;
  };
  let { run, active, onpick }: Props = $props();
</script>

<button
  type="button"
  class="w-full text-left px-4 py-3 border-b border-slate-200 dark:border-slate-800 transition-colors"
  class:bg-slate-100={active}
  class:dark:bg-slate-800={active}
  class:ring-1={active}
  class:ring-inset={active}
  class:ring-emerald-500={active}
  class:hover:bg-slate-50={!active}
  class:dark:hover:bg-slate-800={!active}
  onclick={() => onpick(runKey(run))}
>
  <div class="flex items-center gap-2 text-xs">
    <span class={"px-1.5 py-0.5 rounded text-[10px] uppercase tracking-wider " + statusBadgeClass(run.status)}>
      {statusLabel(run.status)}
    </span>
    <span class="ml-auto text-slate-500 tabular-nums">{fmtDuration(run)}</span>
  </div>
  <div class="mt-1 flex items-center gap-2 text-[11px] font-mono text-slate-500">
    <span class="px-1.5 py-0.5 rounded bg-slate-200 dark:bg-slate-700 text-slate-700 dark:text-slate-300">{shortID(runKey(run))}</span>
    <span>{fmtTimestamp(run.started_at)}</span>
  </div>
</button>
