<script lang="ts">
  import SettingsTab from "./tabs/SettingsTab.svelte";

  type Props = {
    open: boolean;
    workflowID: string;
    workflowName: string;
    onClose: () => void;
  };

  let { open, workflowID, workflowName, onClose }: Props = $props();

  function onKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") onClose();
  }
</script>

<svelte:window onkeydown={(e) => { if (open) onKeydown(e); }} />

{#if open}
  <!-- Backdrop -->
  <div
    class="fixed inset-0 z-[60] flex items-center justify-center p-4 bg-black-900/40 dark:bg-navy-900/60 backdrop-blur-sm"
    role="dialog"
    aria-modal="true"
    onclick={onClose}
  >
    <!-- Panel — wide, not full-screen -->
    <div
      class="w-full max-w-2xl max-h-[80vh] flex flex-col rounded-xl bg-white-100 dark:bg-navy-800 border border-white-300 dark:border-navy-600 shadow-2xl overflow-hidden"
      onclick={(e) => e.stopPropagation()}
      role="presentation"
    >
      <!-- Header -->
      <header class="flex items-center justify-between px-5 py-3.5 border-b border-white-300 dark:border-navy-600 shrink-0">
        <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">
          Workflow settings for <span class="font-mono">{workflowName}</span>
        </h2>
        <button
          type="button"
          class="flex items-center justify-center w-6 h-6 rounded text-black-700 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-600 hover:text-black-900 dark:hover:text-white-100 transition-colors"
          onclick={onClose}
          aria-label="Close"
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
            <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
          </svg>
        </button>
      </header>

      <!-- Scrollable body -->
      <div class="flex-1 overflow-y-auto">
        <SettingsTab {workflowID} />
      </div>
    </div>
  </div>
{/if}
