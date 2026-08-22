<script lang="ts">
  /* The only visible trace of auto-saving.

     Two deliberate delays keep it from flickering while you type:
     - "Saving…" waits SHOW_SAVING_AFTER_MS before appearing, so a save that
       lands quickly (the normal case) never paints a spinner at all.
     - "Saved" holds for SAVED_HOLD_MS, then settles into a relative
       timestamp instead of vanishing, so the row never becomes empty and
       the header cannot reflow. */
  import type { SaveStatus } from "$lib/autosave.js";

  type Props = {
    status: SaveStatus;
    onRetry: () => void;
  };
  let { status, onRetry }: Props = $props();

  const SHOW_SAVING_AFTER_MS = 400;
  const SAVED_HOLD_MS = 2000;

  let showSaving = $state(false);
  let showSavedPulse = $state(false);
  /* Re-rendered only when a save lands, never on a timer: a ticking clock
     would repaint the header every second for no new information. */
  let savedLabel = $state("");

  $effect(() => {
    const s = status.state;
    if (s !== "saving") {
      showSaving = false;
      return;
    }
    const t = setTimeout(() => { showSaving = true; }, SHOW_SAVING_AFTER_MS);
    return () => clearTimeout(t);
  });

  $effect(() => {
    if (status.state !== "saved") return;
    showSavedPulse = true;
    savedLabel = relativeLabel(status.savedAt);
    const t = setTimeout(() => { showSavedPulse = false; }, SAVED_HOLD_MS);
    return () => clearTimeout(t);
  });

  function relativeLabel(at?: number): string {
    if (!at) return "";
    const mins = Math.floor((Date.now() - at) / 60000);
    if (mins < 1) return "just now";
    if (mins < 60) return `${mins}m ago`;
    return `${Math.floor(mins / 60)}h ago`;
  }

  const text = $derived.by(() => {
    if (status.state === "error") return "Not saved";
    if (showSaving) return "Saving…";
    if (showSavedPulse) return "Saved";
    if (status.savedAt) return `Saved ${savedLabel}`;
    return "";
  });
</script>

<!-- Fixed min-width: the label changes length as it cycles, and letting the
     row resize would shift the tab strip next to it. -->
<div class="flex min-w-[136px] items-center justify-end gap-2" aria-live="polite" data-testid="save-status">
  {#if status.state === "error"}
    <span class="inline-flex items-center gap-1.5 text-xs font-medium text-neg-400">
      <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="8" cy="8" r="6.5"></circle>
        <path d="M8 5v4M8 11h0" stroke-linecap="round"></path>
      </svg>
      {text}
    </span>
    <button
      type="button"
      onclick={onRetry}
      class="rounded-lg border border-neg-400 px-2 py-1 text-xs font-medium text-neg-400 transition-colors hover:bg-neg-100"
    >Retry</button>
  {:else if text}
    <span class="text-xs text-black-700 transition-opacity duration-150 dark:text-black-600">{text}</span>
  {/if}
</div>
