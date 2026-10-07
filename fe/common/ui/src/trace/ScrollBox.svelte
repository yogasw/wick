<script lang="ts">
  import type { Snippet } from "svelte";

  /* Caps a trace block at maxHeight so one long result cannot push the
     rest of the thread off screen. Expand lifts the cap in place. */
  type Props = { maxHeight?: number; children: Snippet };
  let { maxHeight = 320, children }: Props = $props();

  let el = $state<HTMLDivElement | null>(null);
  let expanded = $state(false);
  let overflowing = $state(false);

  function measure(): void {
    if (el) overflowing = el.scrollHeight > maxHeight + 1;
  }

  $effect(() => {
    if (!el) return;
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  });
</script>

<div data-trace-scrollbox>
  <div
    bind:this={el}
    class="overflow-auto"
    style={expanded ? "" : `max-height:${maxHeight}px`}
    data-expanded={expanded}
  >
    {@render children()}
  </div>
  {#if overflowing || expanded}
    <button
      type="button"
      data-trace-expand
      onclick={() => { expanded = !expanded; }}
      class="w-full border-t border-white-300 dark:border-navy-600 py-1 text-center text-[10px] font-medium text-link-400 hover:bg-white-200 dark:hover:bg-navy-700"
    >{expanded ? "Collapse" : "Expand"}</button>
  {/if}
</div>
