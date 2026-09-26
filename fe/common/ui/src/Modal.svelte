<script module lang="ts">
  /* Which modals are open, innermost last, shared by every instance.

     A stack rather than a flag because dialogs nest: a ConfirmDialog opened
     from inside another modal is itself a Modal, and the two of them then
     disagree about two things. Scroll — restoring the page when the INNER one
     closes would unlock it while the outer still covers it. And Escape —
     `svelte:window` puts a handler on the window for EVERY open modal, so one
     keypress reached both: the outer's handler opened the confirmation and the
     inner's closed it again in the same dispatch, which looked to the user
     like Escape doing nothing at all. Only the top of the stack answers. */
  let stack: number[] = [];
  let seq = 0;
</script>

<script lang="ts">
  /* Modal shell: backdrop + centered panel + header (title/close) + body +
     optional footer. Esc and backdrop click close. Accessible
     (role=dialog + aria-modal + tabindex). Consumes design-system tokens.

     It also takes focus on open and gives it back on close, and holds the
     page still underneath. Those two are what separate a modal from a div
     that looks like one: without them a keyboard lands behind the dialog,
     and closing drops the reader wherever the page happened to scroll to. */
  import type { Snippet } from "svelte";

  type Size = "sm" | "md" | "lg" | "xl" | "2xl";
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
    // 2xl is for reading, not for a form: a page of markdown in a 4xl column
    // is still a narrow strip on a wide screen (Yoga, 2026-09-26: "kecil bet
    // gini"). Everything below it stays as it was.
    "2xl": "max-w-6xl",
  };

  function onKeydown(e: KeyboardEvent) {
    if (e.key !== "Escape" || !open) return;
    // Only the innermost dialog closes. A modal that has just been opened by
    // this very keypress is not on the stack yet, which is exactly right: it
    // did not exist when the key went down.
    if (stack[stack.length - 1] !== token) return;
    onClose();
  }

  // The panel itself, so opening can move focus into it.
  let panel = $state<HTMLElement | null>(null);
  // Bookkeeping for the effect below — plain lets, since nothing renders from
  // them. `token` is this instance's place in the shared stack.
  let returnTo: HTMLElement | null = null;
  let wasOpen = false;
  let token = 0;

  function leave(): void {
    stack = stack.filter((t) => t !== token);
    if (stack.length === 0) document.body.style.overflow = "";
  }

  $effect(() => {
    if (open === wasOpen) return;
    wasOpen = open;
    if (open) {
      // Captured before the panel steals it — this is the control the user
      // pressed, and it is where they expect to be when the dialog closes.
      returnTo = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      panel?.focus();
      token = ++seq;
      stack.push(token);
      document.body.style.overflow = "hidden";
      return;
    }
    leave();
    // A row that was deleted while the dialog was up is no longer in the
    // document; focusing it is a no-op rather than an error.
    returnTo?.focus();
    returnTo = null;
  });

  // A component torn down while open would otherwise leave the page locked.
  $effect(() => () => {
    if (wasOpen) leave();
  });
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
      bind:this={panel}
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
      <!-- overflow-y-auto, not overflow-auto: `auto` on both axes made a short
           body show a horizontal-plus-vertical scrollbar pair as soon as any
           child overflowed by a pixel. min-h-0 lets it shrink inside the
           flex column so the cap applies to the body, not the whole dialog. -->
      <div class="min-h-0 overflow-y-auto px-4 py-3">
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
