<script lang="ts">
  /* The panel's card. Every block on every tab is one of these, and the
     `scope` slot in its header is not decoration: store-wide numbers and
     per-project numbers look identical once they are in a card, so each card
     states which it is (PLAN §13.5 point 1). */
  import type { Snippet } from "svelte";

  type Props = {
    title: string;
    scope?: string;
    note?: string;
    actions?: Snippet;
    children: Snippet;
  };
  let { title, scope, note, actions, children }: Props = $props();
</script>

<section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
  <div
    class="flex flex-wrap items-center justify-between gap-2 border-b border-white-300 dark:border-navy-600 px-5 py-3"
  >
    <div class="flex min-w-0 flex-wrap items-baseline gap-2">
      <h2 class="text-sm font-medium text-black-900 dark:text-white-100">{title}</h2>
      {#if scope}
        <span class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">{scope}</span>
      {/if}
    </div>
    {#if actions}
      <div class="flex flex-wrap items-center gap-2">{@render actions()}</div>
    {/if}
  </div>
  {#if note}
    <p class="border-b border-white-300 dark:border-navy-600 px-5 py-2 text-xs leading-relaxed text-black-700 dark:text-black-600">
      {note}
    </p>
  {/if}
  {@render children()}
</section>
