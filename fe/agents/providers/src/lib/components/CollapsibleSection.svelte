<script lang="ts">
  import type { Snippet } from "svelte";
  import { loadOpen, saveOpen } from "$lib/collapse.js";

  /* One Detail-page card whose body hides behind its header row. The
     header always shows the title plus an optional summary, so a closed
     section still says what is in it. Open/closed is remembered per
     browser under storageKey. */
  type Props = {
    title: string;
    storageKey: string;
    defaultOpen?: boolean;
    testid?: string;
    summary?: Snippet;
    /* Body wrapper classes; "" when the content brings its own padding. */
    bodyClass?: string;
    children: Snippet;
  };
  let { title, storageKey, defaultOpen = false, testid, summary, bodyClass = "p-5 space-y-3", children }: Props = $props();

  let open = $state(loadOpen(storageKey, defaultOpen));
  function toggle() {
    open = !open;
    saveOpen(storageKey, open);
  }
</script>

<div data-testid={testid} data-open={open ? "1" : "0"} class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
  <div
    role="button"
    tabindex="0"
    aria-expanded={open}
    onclick={toggle}
    onkeydown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); toggle(); } }}
    class="flex items-center gap-3 px-5 py-3 cursor-pointer select-none bg-white-200 dark:bg-navy-800 hover:bg-white-300 dark:hover:bg-navy-600 transition-colors {open ? 'border-b border-white-300 dark:border-navy-600' : ''}"
  >
    <svg class="h-3.5 w-3.5 shrink-0 text-black-600 transition-transform {open ? 'rotate-90' : ''}" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"></path></svg>
    <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">{title}</h3>
    {#if summary}
      <span class="min-w-0 truncate text-xs text-black-700 dark:text-black-600">{@render summary()}</span>
    {/if}
  </div>
  {#if open}
    <div class={bodyClass}>{@render children()}</div>
  {/if}
</div>
