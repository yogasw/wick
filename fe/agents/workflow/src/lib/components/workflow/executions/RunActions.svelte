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
  };
  let { runID, runDetail, onReplay }: Props = $props();

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
</script>

<div class="flex items-center gap-1.5 text-xs">
  <button
    type="button"
    class="px-2 py-1 rounded border border-slate-300 dark:border-slate-700 hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-700 dark:text-slate-200"
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
    class="px-2 py-1 rounded border border-slate-300 dark:border-slate-700 hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-700 dark:text-slate-200"
    onclick={exportJSON}
    disabled={!runDetail}
    title="Download the full run state as run-<id>.json"
  >
    export JSON
  </button>
</div>
