<script lang="ts">
  import Toolbar from "./Toolbar.svelte";
  import Palette from "./Palette.svelte";
  import Canvas from "./Canvas.svelte";
  import NodeDetailModal from "./NodeDetailModal.svelte";
  import TriggerDetailModal from "./TriggerDetailModal.svelte";
  import BottomTabs from "./BottomTabs.svelte";
  import ExecutionsPanel from "./ExecutionsPanel.svelte";
  import { writable } from "svelte/store";

  // Top-level tab toggle between Editor + Executions panel.
  const topTab = writable<"editor" | "executions">("editor");
  import { loadWorkflow, draftWorkflow, paletteOpen, lastRunSummary } from "$lib/stores/editor";
  import { connectSSE, disconnectSSE } from "$lib/stores/sse";
  import { workflowAPI, type RunSummary } from "$lib/api/workflow";
  import type { WorkflowVersion } from "$lib/types/workflow";

  type Props = { workflowID: string };
  let { workflowID }: Props = $props();

  let runs = $state<RunSummary[]>([]);
  let versions = $state<WorkflowVersion[]>([]);

  $effect(() => {
    void loadWorkflow(workflowID);
    void refreshRuns();
    void refreshVersions();
    // Subscribe to the live event stream for this workflow so node
    // status overlays (Gap D) + Logs tab content (Gap E) update in
    // real time while a run is firing.
    connectSSE(workflowID);
    return () => disconnectSSE();
  });


  async function refreshRuns() {
    try {
      const res = await workflowAPI.runs(workflowID);
      runs = res.runs ?? [];
    } catch { /* endpoint may not be JSON-mode yet — Phase 2 fixes that */ }
  }

  async function refreshVersions() {
    try {
      const res = await workflowAPI.versions(workflowID);
      versions = res.versions ?? [];
    } catch { /* DB not wired in this env */ }
  }

  async function onRestoreVersion(versionID: number) {
    await workflowAPI.restoreVersion(workflowID, versionID);
    await loadWorkflow(workflowID);
    await refreshVersions();
  }
</script>

<div class="flex flex-col h-screen relative">
  {#if $lastRunSummary}
    <div class="absolute top-3 left-1/2 -translate-x-1/2 z-50 flex items-center gap-2 px-4 py-2 rounded-full text-xs font-medium shadow-lg"
         class:bg-emerald-500={$lastRunSummary.status === "success"}
         class:text-white={true}
         class:bg-rose-500={$lastRunSummary.status === "failed"}>
      <span>{$lastRunSummary.status === "success" ? "✓" : "✗"}</span>
      <span>Run {$lastRunSummary.status} in {$lastRunSummary.durationMs}ms</span>
    </div>
  {/if}
  <Toolbar topTab={topTab} />

  {#if $topTab === "editor"}
    <div class="flex flex-1 min-h-0 relative">
      <Canvas />
      {#if $paletteOpen}
        <Palette />
      {/if}
    </div>
    <NodeDetailModal />
    <TriggerDetailModal />
    <BottomTabs runs={runs} versions={versions} onRestoreVersion={onRestoreVersion} />
  {:else}
    <ExecutionsPanel workflowID={workflowID} />
  {/if}
</div>
