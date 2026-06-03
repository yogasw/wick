<script lang="ts">
  // Generic confirm dialog used by destructive actions (Delete
  // workflow, Delete node, Discard draft, Unpublish). Centralised so
  // every blocking confirmation looks the same — title + body + two
  // buttons (Cancel + Confirm). `destructive=true` swaps the confirm
  // button to red.
  type Props = {
    open: boolean;
    title: string;
    body?: string;
    confirmLabel?: string;
    cancelLabel?: string;
    destructive?: boolean;
    onConfirm: () => void;
    onCancel: () => void;
  };

  let {
    open,
    title,
    body = "",
    confirmLabel = "Confirm",
    cancelLabel = "Cancel",
    destructive = false,
    onConfirm,
    onCancel,
  }: Props = $props();
</script>

{#if open}
  <div
    class="fixed inset-0 z-[60] flex items-center justify-center bg-white-100 dark:bg-navy-800/60 backdrop-blur-sm"
    role="dialog"
    aria-modal="true"
    onclick={onCancel}
    onkeydown={(e) => e.key === "Escape" && onCancel()}
  >
    <div
      class="w-[440px] max-w-[92vw] rounded-lg bg-white dark:bg-navy-700 text-slate-900 dark:text-white-100 shadow-2xl border border-slate-200 dark:border-navy-600"
      onclick={(e) => e.stopPropagation()}
      role="presentation"
    >
      <header class="px-5 py-4 border-b border-slate-200 dark:border-navy-600">
        <h3 class="text-sm font-semibold">{title}</h3>
      </header>
      {#if body}
        <div class="px-5 py-4 text-sm text-black-600 dark:text-black-600">{body}</div>
      {/if}
      <footer class="flex justify-end gap-2 px-5 py-3 border-t border-slate-200 dark:border-navy-600">
        <button
          class="px-3 py-1.5 rounded text-xs font-medium text-black-500 dark:text-black-600 hover:bg-white-200 dark:hover:bg-white-300 dark:bg-navy-600"
          onclick={onCancel}
        >{cancelLabel}</button>
        <button
          class="px-3 py-1.5 rounded text-xs font-semibold text-white-100"
          class:bg-rose-500={destructive}
          class:hover:bg-rose-600={destructive}
          class:bg-emerald-500={!destructive}
          class:hover:bg-emerald-600={!destructive}
          onclick={onConfirm}
        >{confirmLabel}</button>
      </footer>
    </div>
  </div>
{/if}
