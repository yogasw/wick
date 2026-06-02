<script lang="ts">
  import Toolbar from "./Toolbar.svelte";
  import Palette from "./Palette.svelte";
  import Canvas from "./Canvas.svelte";
  import NodeDetailModal from "./NodeDetailModal.svelte";
  import TriggerDetailModal from "./TriggerDetailModal.svelte";
  import BottomTabs from "./BottomTabs.svelte";
  import ExecutionsPanel from "./ExecutionsPanel.svelte";
  import ToastHost from "$lib/components/shared/ToastHost.svelte";
  import { writable } from "svelte/store";

  // Top-level tab toggle between Editor + Executions panel.
  const topTab = writable<"editor" | "executions">("editor");
  import {
    loadWorkflow,
    draftWorkflow,
    paletteOpen,
    lastRunSummary,
    saveDraft,
    selectedNodeID,
    selectedNodeIDs,
    detailNodeID,
    detailTriggerID,
    removeNode,
  } from "$lib/stores/editor";
  import { get } from "svelte/store";
  import { loadCatalog } from "$lib/stores/catalog";
  import { connectSSE, disconnectSSE } from "$lib/stores/sse";
  import { workflowAPI, type RunSummary } from "$lib/api/workflow";
  import type { WorkflowVersion } from "$lib/types/workflow";

  type Props = { workflowID: string };
  let { workflowID }: Props = $props();

  let runs = $state<RunSummary[]>([]);
  let versions = $state<WorkflowVersion[]>([]);

  $effect(() => {
    void loadWorkflow(workflowID);
    void loadCatalog();
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

  // Global keyboard shortcuts. We only short-circuit when focus is in a
  // form control so users can still Ctrl+S inside a textarea to commit
  // the draft, but Backspace inside an input field won't nuke the
  // selected canvas node.
  function isTypingInForm(target: EventTarget | null): boolean {
    if (!(target instanceof HTMLElement)) return false;
    const tag = target.tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
    if (target.isContentEditable) return true;
    return false;
  }

  function onKeydown(e: KeyboardEvent) {
    const inForm = isTypingInForm(e.target);
    // Ctrl/Cmd+S — manual save, always available (overrides browser save).
    if ((e.ctrlKey || e.metaKey) && (e.key === "s" || e.key === "S")) {
      e.preventDefault();
      void saveDraft({ silent: false });
      return;
    }
    // Esc — close palette / inspector modals.
    if (e.key === "Escape") {
      if (get(detailNodeID)) {
        detailNodeID.set(null);
        return;
      }
      if (get(detailTriggerID)) {
        detailTriggerID.set(null);
        return;
      }
      if (get(paletteOpen)) {
        paletteOpen.set(false);
        return;
      }
      if (get(selectedNodeID)) {
        selectedNodeID.set(null);
        selectedNodeIDs.set(new Set());
        return;
      }
    }
    // Delete / Backspace — remove selected node(s). Skip if user is
    // typing in any form control so it doesn't eat the backspace key.
    if (!inForm && (e.key === "Delete" || e.key === "Backspace")) {
      const multi = get(selectedNodeIDs);
      if (multi && multi.size > 0) {
        e.preventDefault();
        for (const id of multi) removeNode(id);
        selectedNodeIDs.set(new Set());
        selectedNodeID.set(null);
        return;
      }
      const one = get(selectedNodeID);
      if (one) {
        e.preventDefault();
        removeNode(one);
        selectedNodeID.set(null);
      }
    }
  }
</script>

<svelte:window onkeydown={onKeydown} />

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

<ToastHost />
