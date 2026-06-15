<script lang="ts">
  /* Modal shell: backdrop + centered panel + header (title/close) + body +
     optional footer. Esc and backdrop click close. Accessible
     (role=dialog + aria-modal + tabindex). Consumes design-system tokens. */
  import type { Snippet } from "svelte";

  type Size = "sm" | "md" | "lg" | "xl";
  type Props = {
    open: boolean;
    title?: string;
    onClose: () => void;
    size?: Size;
    closeOnBackdrop?: boolean;
    header?: Snippet;
    children: Snippet;
    footer?: Snippet;
  };

  let {
    open,
    title,
    onClose,
    size = "md",
    closeOnBackdrop = true,
    header,
    children,
    footer,
  }: Props = $props();

  const widths: Record<Size, string> = {
    sm: "max-w-sm",
    md: "max-w-md",
    lg: "max-w-2xl",
    xl: "max-w-4xl",
  };

  function onKeydown(e: KeyboardEvent) {
    if (e.key === "Escape" && open) {
      onClose();
    }
  }
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events -- backdrop click is an enhancement; Escape (svelte:window) is the keyboard close path -->
  <div
    class="fixed inset-0 z-[60] flex items-center justify-center p-4 bg-black-900/40 dark:bg-navy-900/60 backdrop-blur-sm"
    role="presentation"
    onclick={() => { if (closeOnBackdrop) onClose(); }}
  >
    <div
      class="flex max-h-[90vh] w-full {widths[size]} flex-col overflow-hidden rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-lg"
      role="dialog"
      aria-modal="true"
      aria-label={title}
      tabindex="-1"
      onclick={(e) => e.stopPropagation()}
    >
      <div class="flex items-center justify-between border-b border-white-300 dark:border-navy-600 px-4 py-3">
        {#if header}
          {@render header()}
        {:else}
          <span class="text-sm font-semibold text-black-900 dark:text-white-100">{title}</span>
        {/if}
        <button
          type="button"
          class="text-lg leading-none text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100"
          aria-label="Close"
          onclick={onClose}
        >×</button>
      </div>
      <div class="overflow-auto px-4 py-3">
        {@render children()}
      </div>
      {#if footer}
        <div class="flex items-center justify-end gap-2 border-t border-white-300 dark:border-navy-600 px-4 py-3">
          {@render footer()}
        </div>
      {/if}
    </div>
  </div>
{/if}
