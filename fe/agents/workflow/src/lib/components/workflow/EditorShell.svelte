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
    removeTrigger,
    savePinnedTrigger,
    searchOpen,
  } from "$lib/stores/editor";
  import { toastOk } from "$lib/stores/toast";
  import { get } from "svelte/store";
  import { loadCatalog } from "$lib/stores/catalog";
  import { connectSSE, disconnectSSE } from "$lib/stores/sse";
  import { workflowAPI } from "$lib/api/workflow";
  import type { WorkflowVersion } from "$lib/types/workflow";

  type Props = { workflowID: string };
  let { workflowID }: Props = $props();

  let versions = $state<WorkflowVersion[]>([]);

  $effect(() => {
    void loadWorkflow(workflowID);
    void loadCatalog();
    void refreshVersions();
    // Subscribe to the live event stream for this workflow so node
    // status overlays + Logs tab content update in real time while a
    // run is firing. The Executions panel does its own polling for
    // the runs list, so we don't need a top-level refresh here.
    connectSSE(workflowID);
    return () => disconnectSSE();
  });

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

  // Replay-to-editor: switch the top tab back to Editor and pin the
  // trigger that fired the picked run so the next Execute click
  // reuses it. Never fires the run automatically — that's the team
  // rule: replay = navigate, not auto-execute.
  function onReplay(triggerID: string | null) {
    if (triggerID) savePinnedTrigger(workflowID, triggerID);
    topTab.set("editor");
    toastOk(
      "Replay prefilled",
      triggerID
        ? "Trigger pinned — hit Execute to re-run."
        : "Open the Editor and hit Execute to re-run.",
    );
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
    // Ctrl/Cmd+K — toggle the search overlay. The overlay's own
    // Escape handler closes it when open, so we only flip from
    // closed → open here.
    if ((e.ctrlKey || e.metaKey) && (e.key === "k" || e.key === "K")) {
      e.preventDefault();
      searchOpen.update((v) => !v);
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
    // Delete / Backspace — remove selected node(s) or trigger(s). The
    // selection set mixes both ids; trigger entries live on
    // `workflow.triggers` so we need a separate removeTrigger call,
    // otherwise selecting "everything" silently leaves the trigger
    // behind (which is what the user saw with Select-All + Delete).
    if (!inForm && (e.key === "Delete" || e.key === "Backspace")) {
      const wf = get(draftWorkflow);
      const triggerIDs = new Set((wf?.triggers ?? []).map((t) => t.id).filter(Boolean) as string[]);
      const removeOne = (id: string) => {
        if (triggerIDs.has(id)) removeTrigger(id);
        else removeNode(id);
      };
      const multi = get(selectedNodeIDs);
      if (multi && multi.size > 0) {
        e.preventDefault();
        for (const id of multi) removeOne(id);
        selectedNodeIDs.set(new Set());
        selectedNodeID.set(null);
        return;
      }
      const one = get(selectedNodeID);
      if (one) {
        e.preventDefault();
        removeOne(one);
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
    <BottomTabs workflowID={workflowID} versions={versions} onRestoreVersion={onRestoreVersion} />
  {:else}
    <ExecutionsPanel workflowID={workflowID} onReplay={onReplay} />
  {/if}
</div>

<ToastHost />
