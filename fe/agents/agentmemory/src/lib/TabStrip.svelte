<script lang="ts">
  /* The panel's tab strip. `enabled` marks the tabs that have content; the
     rest still render (so the shape of the feature is visible) but say so on
     hover instead of pretending to be ready. */
  import type { Tab } from "./types.js";

  type Props = {
    tabs: { id: Tab; label: string; enabled: boolean }[];
    active: Tab;
    onSelect: (t: Tab) => void;
  };
  let { tabs, active, onSelect }: Props = $props();
</script>

<div
  class="flex flex-wrap rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-0.5"
  role="tablist"
>
  {#each tabs as t (t.id)}
    <button
      type="button"
      role="tab"
      aria-selected={active === t.id}
      title={t.enabled ? undefined : `${t.label} is not built yet`}
      onclick={() => onSelect(t.id)}
      class={`rounded-md px-3 py-1.5 text-[0.8125rem] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-green-200 dark:focus-visible:ring-green-800 ${
        active === t.id
          ? "bg-green-200 dark:bg-green-800 text-green-700 dark:text-green-300"
          : t.enabled
            ? "text-black-800 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100"
            : "text-black-700 dark:text-black-600 opacity-60"
      }`}
    >
      {t.label}
    </button>
  {/each}
</div>
