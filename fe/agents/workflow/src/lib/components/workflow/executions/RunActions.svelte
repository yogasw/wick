<script lang="ts">
  // Action toolbar for the selected run. Surfaces the operations
  // that used to live inside the bottom "Runs" tab — copy ID,
  // replay-to-editor, export JSON. "Replay" navigates back to the
  // editor with the firing trigger pre-pinned (per the team rule:
  // replay = navigate, never auto-execute).
  import { toastError, toastOk } from "$lib/stores/toast";
  import { downloadJSON, triggerIDOf } from "./runHelpers";

  type Props = {
    runID: string;
    runDetail: any | null;
    onReplay?: (triggerID: string | null) => void;
    onDelete?: (runID: string) => void;
    onRerun?: (runID: string) => void;
  };
  let { runID, runDetail, onReplay, onDelete, onRerun }: Props = $props();

  let showPreview = $state(false);
  const previewText = $derived(runDetail ? JSON.stringify(runDetail, null, 2) : "");

  async function copyJSON() {
    try {
      await navigator.clipboard.writeText(previewText);
      toastOk("Run JSON copied");
    } catch (e) {
      toastError("Copy failed", e instanceof Error ? e.message : String(e));
    }
  }

  async function copyID() {
    try {
      await navigator.clipboard.writeText(runID);
      toastOk("Run ID copied");
    } catch (e) {
      toastError("Copy failed", e instanceof Error ? e.message : String(e));
    }
  }

  function exportJSON() {
    if (!runDetail) {
      toastError("Export failed", "Run detail hasn't loaded yet.");
      return;
    }
    try {
      downloadJSON(`run-${runID}.json`, runDetail);
    } catch (e) {
      toastError("Export failed", e instanceof Error ? e.message : String(e));
    }
  }

  function replay() {
    onReplay?.(triggerIDOf(runDetail));
  }

  function del() {
    if (!confirm("Delete this run log? Its files are removed and this cannot be undone.")) return;
    onDelete?.(runID);
  }

  function rerun() {
    onRerun?.(runID);
  }
</script>

<div class="flex flex-wrap items-center gap-1.5 text-xs">
  <button
    type="button"
    class="px-2 py-1 rounded border border-emerald-500 bg-emerald-500 text-white-100 hover:bg-emerald-600 disabled:opacity-40"
    onclick={rerun}
    disabled={!runDetail}
    title="Re-run this workflow with the same trigger input"
  >
    re-run
  </button>
  <button
    type="button"
    class="px-2 py-1 rounded border border-slate-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 hover:bg-slate-100 dark:hover:bg-navy-600 text-black-700 dark:text-black-300"
    onclick={copyID}
    title="Copy run id to clipboard"
  >
    copy ID
  </button>
  <button
    type="button"
    class="px-2 py-1 rounded border border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 hover:bg-emerald-500/20"
    onclick={replay}
    title="Open the editor and pin this run's trigger so you can re-fire"
    disabled={!runDetail}
  >
    replay to editor
  </button>
  <button
    type="button"
    class="px-2 py-1 rounded border border-slate-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 hover:bg-slate-100 dark:hover:bg-navy-600 text-black-700 dark:text-black-300"
    onclick={exportJSON}
    disabled={!runDetail}
    title="Download the full run state as run-<id>.json"
  >
    export JSON
  </button>
  <button
    type="button"
    class="px-2 py-1 rounded border border-slate-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 hover:bg-slate-100 dark:hover:bg-navy-600 text-black-700 dark:text-black-300"
    onclick={() => (showPreview = true)}
    disabled={!runDetail}
    title="Preview the full run JSON without downloading"
  >
    preview JSON
  </button>
  <button
    type="button"
    class="px-2 py-1 rounded border border-rose-500/40 bg-rose-500/10 text-rose-700 dark:text-rose-300 hover:bg-rose-500/20"
    onclick={del}
    disabled={!runDetail}
    title="Delete this run log (removes its files)"
  >
    delete
  </button>
</div>

{#if showPreview}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm p-4"
    onclick={(e) => { if (e.target === e.currentTarget) showPreview = false; }}
  >
    <div class="w-full max-w-2xl max-h-[80vh] flex flex-col rounded-xl bg-white-100 dark:bg-navy-700 border border-white-300 dark:border-navy-600 shadow-lg overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-white-300 dark:border-navy-600">
        <h2 class="text-sm font-semibold text-black-800 dark:text-white-100">Run JSON</h2>
        <div class="flex items-center gap-2 text-xs">
          <button
            type="button"
            class="px-2 py-1 rounded border border-slate-300 dark:border-navy-600 hover:bg-white-200 dark:hover:bg-navy-600 text-black-500 dark:text-white-100"
            onclick={copyJSON}
          >
            copy
          </button>
          <button
            type="button"
            class="px-2 py-1 rounded border border-slate-300 dark:border-navy-600 hover:bg-white-200 dark:hover:bg-navy-600 text-black-500 dark:text-white-100"
            onclick={() => (showPreview = false)}
          >
            close
          </button>
        </div>
      </div>
      <pre class="flex-1 overflow-auto px-4 py-3 text-[11px] font-mono text-black-800 dark:text-white-100 whitespace-pre-wrap">{previewText}</pre>
    </div>
  </div>
{/if}
