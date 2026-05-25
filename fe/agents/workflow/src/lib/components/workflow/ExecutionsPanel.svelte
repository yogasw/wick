<script lang="ts">
  // Executions tab — runs list left, run detail right. Mirrors the
  // legacy editor's /executions panel: SUCCESS/FAILED pill, run id,
  // datetime, duration. Clicking a row loads the run state into the
  // right panel (NODES summary + EVENTS timeline).
  import { onMount } from "svelte";
  import { workflowAPI, type RunSummary } from "$lib/api/workflow";

  type Props = { workflowID: string };
  let { workflowID }: Props = $props();

  let runs = $state<RunSummary[]>([]);
  let selectedRunID = $state<string | null>(null);
  let runDetail = $state<any | null>(null);
  let auto = $state(true);
  let loading = $state(false);

  async function refresh() {
    if (loading) return;
    loading = true;
    try {
      const res = await workflowAPI.runs(workflowID);
      runs = res.runs ?? [];
    } catch (e) {
      console.error("runs fetch failed:", e);
    } finally {
      loading = false;
    }
  }

  async function loadRun(runID: string) {
    selectedRunID = runID;
    runDetail = null;
    try {
      runDetail = await workflowAPI.runState(workflowID, runID);
    } catch (e) {
      console.error("run state fetch failed:", e);
    }
  }

  onMount(() => {
    refresh();
    const id = setInterval(() => {
      if (auto) refresh();
    }, 3000);
    return () => clearInterval(id);
  });

  function fmt(ts?: string) {
    if (!ts) return "—";
    return ts.replace("T", " ").slice(0, 19);
  }
</script>

<div class="flex flex-1 min-h-0">
  <!-- Left: runs list. -->
  <aside class="shrink-0 border-r border-slate-200 dark:border-slate-700 flex flex-col" style="width:380px;">
    <header class="px-4 py-2 border-b border-slate-200 dark:border-slate-700 flex items-center gap-3 text-xs">
      <span class="font-semibold text-slate-700 dark:text-slate-200">{runs.length} RUNS</span>
      <button class="ml-auto text-slate-500 hover:text-slate-900 dark:hover:text-slate-100" onclick={refresh} disabled={loading} title="Refresh">↻</button>
      <label class="flex items-center gap-1 text-slate-500">
        <input type="checkbox" bind:checked={auto} />
        auto
      </label>
    </header>
    <div class="flex-1 overflow-y-auto">
      {#if runs.length === 0}
        <p class="p-4 text-xs text-slate-500">No runs yet.</p>
      {:else}
        {#each runs as r}
          <button
            class="w-full text-left px-4 py-3 border-b border-slate-200 dark:border-slate-800 hover:bg-slate-100 dark:hover:bg-slate-800/60"
            class:bg-slate-100={selectedRunID === r.run_id}
            class:dark:bg-slate-800={selectedRunID === r.run_id}
            onclick={() => loadRun(r.run_id)}
          >
            <div class="flex items-center gap-2 text-xs">
              <span class={`px-1.5 py-0.5 rounded text-[10px] uppercase ${r.status === "success" ? "bg-emerald-500/20 text-emerald-300" : r.status === "failed" ? "bg-rose-500/20 text-rose-300" : "bg-amber-500/20 text-amber-300"}`}>
                {r.status === "success" ? "✓ success" : r.status === "failed" ? "✗ failed" : r.status}
              </span>
              <span class="ml-auto text-slate-500 tabular-nums">{r.finished_at ? "" : "—"}</span>
            </div>
            <div class="mt-1 flex items-center gap-2 text-[11px] font-mono text-slate-500">
              <span class="px-1.5 py-0.5 rounded bg-slate-200 dark:bg-slate-700 text-slate-700 dark:text-slate-300">{r.run_id.slice(0, 8)}</span>
              <span>{fmt(r.started_at)}</span>
            </div>
          </button>
        {/each}
      {/if}
    </div>
  </aside>

  <!-- Right: run detail. -->
  <section class="flex-1 overflow-y-auto p-4">
    {#if !selectedRunID}
      <div class="h-full flex flex-col items-center justify-center text-slate-500 text-sm gap-3">
        <span class="text-2xl">⊜</span>
        <div class="font-medium">Select a run</div>
        <div class="text-xs">Click any execution on the left to inspect its output.</div>
      </div>
    {:else if !runDetail}
      <p class="text-xs text-slate-500">Loading…</p>
    {:else}
      {@const events = runDetail.events ?? []}
      {@const completed = runDetail.completed ?? []}
      {@const failed = runDetail.failed ?? []}
      <header class="flex items-center gap-3 mb-4 text-sm">
        <span class={`px-2 py-0.5 rounded text-[10px] uppercase ${runDetail.status === "success" ? "bg-emerald-500/20 text-emerald-300" : runDetail.status === "failed" ? "bg-rose-500/20 text-rose-300" : "bg-amber-500/20 text-amber-300"}`}>
          {runDetail.status === "success" ? "✓ success" : runDetail.status === "failed" ? "✗ failed" : runDetail.status}
        </span>
        <span class="font-mono text-xs">{selectedRunID}</span>
        <span class="text-slate-500 text-xs">{fmt(runDetail.started_at)}</span>
        <div class="flex-1"></div>
        <a class="text-emerald-500 text-xs hover:underline" href={`/tools/agents/workflows/edit/${workflowID}/runs/${selectedRunID}`} target="_blank">Full detail ↗</a>
      </header>

      <div class="grid grid-cols-2 gap-4">
        <section>
          <div class="text-[11px] font-semibold tracking-wider text-slate-500 mb-2 flex items-center gap-2">
            <span>NODES</span>
            <span class="text-slate-400">{completed.length + failed.length} ran</span>
          </div>
          <ul class="divide-y divide-slate-200 dark:divide-slate-700 text-xs">
            {#each completed as nodeID}
              <li class="flex items-center gap-2 py-1.5">
                <span class="text-emerald-500">✓</span>
                <span class="font-mono truncate flex-1">{nodeID}</span>
              </li>
            {/each}
            {#each failed as nodeID}
              <li class="flex items-center gap-2 py-1.5">
                <span class="text-rose-500">✗</span>
                <span class="font-mono truncate flex-1">{nodeID}</span>
              </li>
            {/each}
          </ul>
        </section>

        <section>
          <div class="text-[11px] font-semibold tracking-wider text-slate-500 mb-2 flex items-center gap-2">
            <span>EVENTS</span>
            <span class="text-slate-400">{events.length}</span>
          </div>
          <ul class="divide-y divide-slate-200 dark:divide-slate-700 text-xs font-mono">
            {#each events as ev}
              <li class="flex items-center gap-2 py-1.5">
                <span class="text-slate-500">{fmt(ev.ts).slice(11)}</span>
                <span class={`px-1.5 py-0.5 rounded text-[10px] ${ev.event?.includes("completed") ? "bg-emerald-500/20 text-emerald-300" : ev.event?.includes("failed") ? "bg-rose-500/20 text-rose-300" : "bg-slate-700/30 text-slate-300"}`}>{ev.event}</span>
                {#if ev.node}<span class="text-slate-400 truncate flex-1">{ev.node}</span>{/if}
              </li>
            {/each}
          </ul>
        </section>
      </div>
    {/if}
  </section>
</div>
