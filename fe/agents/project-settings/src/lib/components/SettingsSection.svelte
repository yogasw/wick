<script lang="ts">
  /* One settings card: title, optional subtitle, optional right-aligned
     action, body. Extracted because the same border/padding/heading trio was
     repeated for every section, and drifted between the two tabs.

     `collapsible` gives a section a disclosure header — used for Advanced,
     where the content (raw meta JSON) is worth keeping but not worth the
     vertical space by default. */
  import type { Snippet } from "svelte";

  type Props = {
    title: string;
    subtitle?: string;
    collapsible?: boolean;
    /** Initial state for a collapsible section. Ignored otherwise. */
    open?: boolean;
    action?: Snippet;
    children: Snippet;
  };
  let { title, subtitle, collapsible = false, open = false, action, children }: Props = $props();

  let expanded = $state(open);
</script>

<section class="rounded-xl border border-white-300 bg-white-100 shadow-sm dark:border-navy-600 dark:bg-navy-700">
  {#if collapsible}
    <button
      type="button"
      aria-expanded={expanded}
      onclick={() => { expanded = !expanded; }}
      class="flex w-full items-center gap-3 px-6 py-4 text-left transition-colors hover:bg-white-200 dark:hover:bg-navy-800 rounded-xl"
    >
      <svg
        viewBox="0 0 16 16"
        class={"h-4 w-4 shrink-0 text-black-700 transition-transform dark:text-black-600 " + (expanded ? "rotate-90" : "")}
        fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"
      >
        <path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"></path>
      </svg>
      <span class="min-w-0 flex-1">
        <span class="block text-sm font-semibold text-black-900 dark:text-white-100">{title}</span>
        {#if subtitle}
          <span class="mt-0.5 block text-xs text-black-700 dark:text-black-600">{subtitle}</span>
        {/if}
      </span>
    </button>
    {#if expanded}
      <div class="border-t border-white-300 px-6 py-4 dark:border-navy-600">
        {@render children()}
      </div>
    {/if}
  {:else}
    <div class="px-6 py-5">
      <div class="mb-4 flex items-start justify-between gap-3">
        <div class="min-w-0">
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">{title}</h2>
          {#if subtitle}
            <p class="mt-1 text-xs leading-relaxed text-black-700 dark:text-black-600">{subtitle}</p>
          {/if}
        </div>
        {#if action}
          <div class="shrink-0">{@render action()}</div>
        {/if}
      </div>
      {@render children()}
    </div>
  {/if}
</section>
