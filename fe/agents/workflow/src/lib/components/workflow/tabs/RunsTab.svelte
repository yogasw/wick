<script lang="ts">
  // Runs tab — list of recent runs with expandable detail rows.
  // Mirrors v1 editor_bottom_tab_runs.templ: status badge, started_at
  // local time, computed duration, per-row actions (copy ID, replay,
  // copy to editor, export JSON, open detail link).
  import type { RunSummary } from "$lib/api/workflow";
  import { workflowAPI } from "$lib/api/workflow";
  import { draftWorkflow, lastRunSummary } from "$lib/stores/editor";
  import { toastError, toastOk } from "$lib/stores/toast";

  type Props = { runs: RunSummary[]; onpick?: (runID: string) => void };
  let { runs, onpick }: Props = $props();

  let expandedID = $state<string | null>(null);

  function fmtTime(iso?: string): string {
    if (!iso) return "—";
    try {
      const d = new Date(iso);
      return d.toLocaleString();
    } catch {
      return iso;
    }
  }

  function fmtDuration(r: RunSummary): string {
    if (!r.started_at) return "—";
    const start = new Date(r.started_at).getTime();
    const endIso = r.ended_at ?? r.finished_at;
    if (!endIso) return "running";
    const end = new Date(endIso).getTime();
    const ms = Math.max(0, end - start);
    if (ms < 1000) return `${ms} ms`;
    if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
    return `${(ms / 60_000).toFixed(1)} m`;
  }

  function statusClass(s: string): string {
    switch ((s ?? "").toLowerCase()) {
      case "success":
      case "succeeded":
      case "ok":
        return "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-300";
      case "failed":
      case "error":
        return "bg-rose-100 text-rose-800 dark:bg-rose-900/40 dark:text-rose-300";
      case "running":
      case "queued":
        return "bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300";
      default:
        return "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300";
    }
  }

  async function copyID(id: string) {
    try {
      await navigator.clipboard.writeText(id);
      toastOk("Run ID copied");
    } catch (e) {
      toastError("Copy failed", e instanceof Error ? e.message : String(e));
    }
  }

  async function replay(runID: string) {
    const wf = $draftWorkflow;
    if (!wf) return;
    try {
      await workflowAPI.runNow(wf.id);
      toastOk("Replay queued", `Triggered manual run; see ${runID} once it lands.`);
    } catch (e) {
      toastError("Replay failed", e instanceof Error ? e.message : String(e));
    }
    void runID;
  }

  async function exportJSON(runID: string) {
    const wf = $draftWorkflow;
    if (!wf) return;
    try {
      const state = await workflowAPI.runState(wf.id, runID);
      const blob = new Blob([JSON.stringify(state, null, 2)], {
        type: "application/json",
      });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `run-${runID}.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      toastError("Export failed", e instanceof Error ? e.message : String(e));
    }
  }

  function openDetail(runID: string) {
    const wf = $draftWorkflow;
    if (!wf) return;
    const url = `/tools/agents/workflows/edit/${encodeURIComponent(wf.id)}/runs/${encodeURIComponent(runID)}`;
    window.open(url, "_blank", "noopener");
  }

  function toggle(id: string) {
    expandedID = expandedID === id ? null : id;
    onpick?.(id);
  }
  void lastRunSummary;
</script>

{#if !runs || runs.length === 0}
  <p class="text-xs text-slate-500 dark:text-slate-400 italic">
    No runs yet. Hit <em>Execute workflow</em> to fire one.
  </p>
{:else}
  <ul class="divide-y divide-slate-200 dark:divide-slate-700">
    {#each runs as r (r.id || r.run_id)}
      {@const isOpen = expandedID === (r.id || r.run_id)}
      <li class="text-xs">
        <button
          type="button"
          class="w-full flex items-center gap-3 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800/40"
          onclick={() => toggle(r.id || r.run_id)}
        >
          <span class="text-slate-400 w-3 text-center">{isOpen ? "▾" : "▸"}</span>
          <span class="px-1.5 py-0.5 rounded text-[10px] uppercase {statusClass(r.status)}">
            {r.status}
          </span>
          <span class="font-mono text-slate-500 dark:text-slate-400 truncate flex-1 text-left">
            {fmtTime(r.started_at)}
          </span>
          <span class="text-slate-500 dark:text-slate-400">{fmtDuration(r)}</span>
        </button>
        {#if isOpen}
          <div class="pl-6 pr-2 py-2 space-y-1.5 bg-slate-50 dark:bg-slate-900/40">
            <div class="font-mono text-[11px] text-slate-600 dark:text-slate-300 break-all">
              {r.id || r.run_id}
            </div>
            {#if r.error}
              <div class="text-[11px] text-rose-700 dark:text-rose-300 whitespace-pre-wrap">
                {r.error}
              </div>
            {/if}
            <div class="flex flex-wrap gap-2 pt-1">
              <button class="text-[11px] text-emerald-600 dark:text-emerald-400 hover:underline" onclick={() => copyID(r.id || r.run_id)}>
                copy ID
              </button>
              <button class="text-[11px] text-emerald-600 dark:text-emerald-400 hover:underline" onclick={() => replay(r.id || r.run_id)}>
                replay
              </button>
              <button class="text-[11px] text-emerald-600 dark:text-emerald-400 hover:underline" onclick={() => exportJSON(r.id || r.run_id)}>
                export JSON
              </button>
              <button class="text-[11px] text-emerald-600 dark:text-emerald-400 hover:underline" onclick={() => openDetail(r.id || r.run_id)}>
                open detail ↗
              </button>
            </div>
          </div>
        {/if}
      </li>
    {/each}
  </ul>
{/if}
