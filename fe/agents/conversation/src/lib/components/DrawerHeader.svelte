<script lang="ts">
  /* Title row shared by the Agents app drawers: title, optional subtitle,
     and the ✕ (Esc does the same, handled by the app shell). */
  type Props = { title: string; subtitle?: string; onClose: () => void };
  let { title, subtitle, onClose }: Props = $props();
</script>

<div class="flex items-start gap-3 border-b border-white-300 px-6 py-4 dark:border-navy-600">
  <div class="min-w-0 flex-1">
    <h2 class="truncate text-base font-semibold text-black-900 dark:text-white-100">{title}</h2>
    {#if subtitle}<p class="truncate text-xs text-black-800 dark:text-black-600">{subtitle}</p>{/if}
  </div>
  <button
    type="button"
    class="flex h-8 w-8 items-center justify-center rounded-lg text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600"
    aria-label="Tutup"
    onclick={onClose}
  >
    <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 4l8 8M12 4l-8 8"></path></svg>
  </button>
</div>
