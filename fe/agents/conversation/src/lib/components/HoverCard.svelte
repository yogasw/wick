<script lang="ts">
  /* A small read-only card shown while the pointer rests on `anchor`: a
     title, label/value rows and an optional footer. Fixed, not absolute, so
     a scrolling thread or the composer's rounded box never clips it; it
     opens below the anchor, above it when there is no room. */
  type Props = {
    anchor: HTMLElement | undefined;
    title: string;
    rows: [string, string][];
    foot?: string;
    tone?: "teal" | "neg";
    testid?: string;
  };
  let { anchor, title, rows, foot = "", tone = "teal", testid = "hover-card" }: Props = $props();

  let card: HTMLDivElement | undefined = $state();
  let pos = $state({ top: -9999, left: -9999 });

  $effect(() => {
    if (!anchor || !card) return;
    const r = anchor.getBoundingClientRect();
    const w = card.offsetWidth, h = card.offsetHeight;
    const left = Math.min(Math.max(8, r.left + r.width / 2 - w / 2), window.innerWidth - w - 8);
    const top = r.bottom + 8 + h > window.innerHeight ? r.top - h - 8 : r.bottom + 8;
    pos = { top: Math.max(8, top), left };
  });
</script>

<div
  bind:this={card}
  role="tooltip"
  data-testid={testid}
  style="top: {pos.top}px; left: {pos.left}px"
  class="pointer-events-none fixed z-50 w-72 rounded-xl border border-white-300 bg-white-100 px-3 py-2.5 text-xs text-black-900 shadow-lg dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
>
  <p class={"mb-1.5 font-semibold " + (tone === "neg" ? "text-neg-400" : "text-teal-600 dark:text-teal-500")}>{title}</p>
  <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
    {#each rows as [k, v] (k)}
      <dt class="text-black-700 dark:text-black-600">{k}</dt>
      <dd class="min-w-0 break-words">{v}</dd>
    {/each}
  </dl>
  {#if foot}
    <p class="mt-2 border-t border-dashed border-white-300 pt-1.5 text-[11px] text-black-700 dark:border-navy-600 dark:text-black-600">{foot}</p>
  {/if}
</div>
