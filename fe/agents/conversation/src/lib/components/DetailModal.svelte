<script lang="ts">
  import { onMount } from "svelte";
  import type { DetailContent } from "../stores/detail.js";

  // Generic detail overlay: shows the full body of a `detail` chip (or any
  // caller) without cluttering the transcript. Content is plain text shown
  // monospaced + wrapped; Copy grabs the body, Esc / backdrop closes.
  type Props = { content: DetailContent | null; onClose: () => void };
  let { content, onClose }: Props = $props();

  let copied = $state(false);

  async function copy() {
    if (!content) return;
    try {
      await navigator.clipboard.writeText(content.body);
      copied = true;
      setTimeout(() => (copied = false), 1500);
    } catch {
      /* clipboard denied — no-op */
    }
  }

  onMount(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
</script>

{#if content}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center p-4"
    role="button"
    tabindex="-1"
    onclick={onClose}
    onkeydown={(e) => e.key === "Enter" && onClose()}
  >
    <div
      class="flex max-h-[80vh] w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-2xl"
      role="dialog"
      aria-modal="true"
      aria-label={content.title}
      tabindex="-1"
      onclick={(e) => e.stopPropagation()}
      onkeydown={(e) => e.stopPropagation()}
    >
      <div class="flex items-center gap-3 border-b border-white-300 dark:border-navy-600 px-5 py-3">
        <h3 class="min-w-0 flex-1 truncate text-sm font-semibold text-black-900 dark:text-white-100">
          {content.title}
        </h3>
        <button
          onclick={copy}
          class="rounded-lg border border-white-400 dark:border-navy-600 px-2.5 py-1 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
        >{copied ? "Copied" : "Copy"}</button>
        <button
          onclick={onClose}
          aria-label="Close"
          class="rounded-lg p-1 text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
        >
          <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path>
          </svg>
        </button>
      </div>
      <div class="overflow-auto px-5 py-4">
        {#if content.body.trim()}
          <pre class="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed text-black-900 dark:text-white-100">{content.body}</pre>
        {:else}
          <p class="text-xs italic text-black-600 dark:text-black-500">No further detail.</p>
        {/if}
      </div>
    </div>
  </div>
{/if}
