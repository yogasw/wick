<script lang="ts">
  /* Breadcrumb trail. Pure rendering — the caller builds the item list and
     wires navigation through onClick. Items with onClick render as links;
     the trailing item (no onClick) renders as the current page label. Pass a
     `current` snippet to render the current item yourself (e.g. an editable
     title) — then every item is treated as a preceding segment. Uses
     design-system tokens (consumes them; does not change the design-system). */
  import type { Snippet } from "svelte";

  export type BreadcrumbItem = {
    label: string;
    onClick?: () => void;
    truncate?: boolean;
  };

  type Props = {
    items: BreadcrumbItem[];
    current?: Snippet;
  };

  let { items, current }: Props = $props();

  const truncateClass = "inline-block max-w-[55vw] truncate align-bottom sm:max-w-[18rem]";
  const linkBase = "whitespace-nowrap hover:text-green-600";
  const currentClass = "inline-block max-w-[55vw] truncate align-bottom text-black-900 dark:text-white-100 sm:max-w-[18rem]";
</script>

<nav
  class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-sm text-black-700 dark:text-black-600"
  aria-label="Breadcrumb"
>
  {#each items as item, i (i)}
    {#if item.onClick}
      <button
        type="button"
        class={item.truncate ? `${truncateClass} hover:text-green-600` : linkBase}
        onclick={item.onClick}
      >{item.label}</button>
    {:else if i === items.length - 1 && !current}
      <span class={currentClass}>{item.label}</span>
    {:else}
      <span class={item.truncate ? truncateClass : "whitespace-nowrap"}>{item.label}</span>
    {/if}
    {#if i < items.length - 1}
      <span aria-hidden="true">/</span>
    {/if}
  {/each}
  {#if current}
    <span aria-hidden="true">/</span>
    {@render current()}
  {/if}
</nav>
