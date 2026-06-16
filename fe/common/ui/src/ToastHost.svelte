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
