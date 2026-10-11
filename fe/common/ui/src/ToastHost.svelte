<script lang="ts">
  // Fixed top-right toast stack — mirror of v1's #wf-toast-host.
  // Newest toast at top; entries auto-dismiss per their ttlMs (set in
  // pushToast); clicking the entry removes it immediately.
  import { toasts, dismissToast, type Toast } from "@wick-fe/common-stores";

  function stateClass(t: Toast): string {
    switch (t.state) {
      case "ok":
        return "border-pos-400 bg-pos-100 text-pos-400";
      case "warn":
        return "border-cau-400 bg-cau-100 text-cau-400";
      case "error":
        return "border-neg-400 bg-neg-100 text-neg-400";
      case "info":
        return "border-white-300 bg-white-100 text-black-700 dark:border-navy-600 dark:bg-navy-700 dark:text-black-600";
    }
  }

  function stateIcon(t: Toast): string {
    switch (t.state) {
      case "ok":
        return "✓";
      case "warn":
        return "⚠";
      case "error":
        return "✕";
      case "info":
        return "";
    }
  }
</script>

<div
  class="fixed top-3 right-3 z-[90] flex flex-col gap-2 max-w-sm pointer-events-none"
  role="region"
  aria-label="Notifications"
>
  {#each $toasts as t (t.id)}
    <button
      type="button"
      class="text-left rounded-md border px-3 py-2 shadow-lg backdrop-blur-sm {stateClass(t)} pointer-events-auto hover:opacity-90 transition-opacity"
      onclick={() => dismissToast(t.id)}
      aria-label="Dismiss notification"
    >
      <div class="flex items-start gap-2">
        {#if t.state === "info"}
          <!-- Work in progress, not an outcome: a spinner, no glyph. -->
          <svg data-testid="toast-spinner" viewBox="0 0 16 16" class="mt-px h-3.5 w-3.5 shrink-0 animate-spin" fill="none" stroke="currentColor" stroke-width="1.75" aria-hidden="true"><path d="M8 2a6 6 0 106 6" stroke-linecap="round"/></svg>
        {:else}
          <span class="text-sm font-semibold leading-tight">{stateIcon(t)}</span>
        {/if}
        <div class="flex-1 min-w-0">
          <div class="text-xs font-semibold leading-snug">{t.title}</div>
          {#if t.body}
            <div class="text-[11px] mt-0.5 opacity-90 whitespace-pre-wrap">{t.body}</div>
          {/if}
        </div>
      </div>
    </button>
  {/each}
</div>
