<script lang="ts">
  // Fixed top-right toast stack — mirror of v1's #wf-toast-host.
  // Newest toast at top; entries auto-dismiss per their ttlMs (set in
  // pushToast); clicking the entry removes it immediately.
  import { toasts, dismissToast, type Toast } from "$lib/stores/toast";

  function stateClass(t: Toast): string {
    switch (t.state) {
      case "ok":
        return "border-emerald-500 bg-emerald-50 dark:bg-emerald-900/40 text-emerald-900 dark:text-emerald-100";
      case "warn":
        return "border-amber-500 bg-amber-50 dark:bg-amber-900/40 text-amber-900 dark:text-amber-100";
      case "error":
        return "border-rose-500 bg-rose-50 dark:bg-rose-900/40 text-rose-900 dark:text-rose-100";
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
        <span class="text-sm font-semibold leading-tight">{stateIcon(t)}</span>
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
