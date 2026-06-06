<script lang="ts">
  // Right pane: the selected run's status header + action toolbar +
  // NODES list + EVENTS timeline. Composed from the smaller per-pane
  // pieces so this file stays a thin orchestrator.
  import RunActions from "./RunActions.svelte";
  import RunNodes from "./RunNodes.svelte";
  import RunEvents from "./RunEvents.svelte";
  import { fmtTimestamp, fmtDuration, statusBadgeClass, statusLabel, triggerIDOf } from "./runHelpers";
  import { draftWorkflow } from "$lib/stores/editor";

  type Props = {
    runID: string;
    runDetail: any | null;
    onReplay?: (triggerID: string | null) => void;
    onDelete?: (runID: string) => void;
    onLoadAllEvents?: () => void;
  };
  let { runID, runDetail, onReplay, onDelete, onLoadAllEvents }: Props = $props();

  // Resolve the firing trigger to a friendly label. trigger_id comes
  // off the event payload (spaWorkflowRunNow stuffs it there); we
  // look it up against the current workflow's triggers list. Older
  // runs predating that may have no trigger_id — fall back to the
  // event type so the header still says something useful.
  const triggerInfo = $derived.by(() => {
    const tid = triggerIDOf(runDetail);
    const wf = $draftWorkflow;
    const trig = tid ? wf?.triggers?.find((t) => t.id === tid) : undefined;
    const label = trig?.label || trig?.id || tid || runDetail?.event?.type || "—";
    const kind = trig?.type ?? runDetail?.event?.type ?? null;
    return { label, kind, id: tid };
  });
</script>

{#if !runDetail}
  <p class="text-xs text-black-700 dark:text-black-600">Loading…</p>
{:else}
  {@const events = runDetail.events ?? []}
  {@const completed = runDetail.completed ?? []}
  {@const failed = runDetail.failed ?? []}
  <header class="mb-4">
    <div class="flex flex-wrap items-center gap-3 text-sm">
      <span class={"px-2 py-0.5 rounded text-[10px] uppercase tracking-wider " + statusBadgeClass(runDetail.status)}>
        {statusLabel(runDetail.status)}
      </span>
      <span class="font-mono text-xs text-black-500 dark:text-white-100 break-all">{runID}</span>
      <div class="flex-1"></div>
      <RunActions {runID} {runDetail} {onReplay} {onDelete} />
    </div>
    <div class="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-black-700 dark:text-black-600">
      <span class="flex items-center gap-1.5">
        <span class="uppercase tracking-wider text-[10px]">trigger</span>
        {#if triggerInfo.kind}
          <span class="px-1.5 py-0.5 rounded bg-slate-200 dark:bg-navy-600 text-black-500 dark:text-black-600 uppercase text-[10px] tracking-wider">{triggerInfo.kind}</span>
        {/if}
        <span class="text-black-500 dark:text-white-100">{triggerInfo.label}</span>
      </span>
      <span class="text-black-700 dark:text-black-500">·</span>
      <span>{fmtTimestamp(runDetail.started_at)}</span>
      <span class="text-black-700 dark:text-black-500">·</span>
      <span class="tabular-nums">{fmtDuration(runDetail)}</span>
    </div>
  </header>

  {#if runDetail.error}
    <div class="mb-4 rounded border border-rose-300 dark:border-rose-700 bg-rose-50 dark:bg-rose-950/40 px-3 py-2 text-xs text-rose-700 dark:text-rose-300 whitespace-pre-wrap">
      ✕ {runDetail.error}
    </div>
  {/if}

  <div class="grid grid-cols-2 gap-4">
    <RunNodes completed={completed} failed={failed} outputs={runDetail.outputs ?? {}} />
    <RunEvents events={events} total={runDetail.events_total ?? events.length} truncated={runDetail.events_truncated ?? false} onLoadAll={onLoadAllEvents} />
  </div>
{/if}
