<script lang="ts">
  /* A grid of labelled numbers. Shared by the store-wide Analytics tab and a
     project's own tab — the drawing is the same, the source never is, so the
     card around it is what states the scope (stats.ts). */
  import type { StatItem } from "./stats.js";

  type Props = {
    items: StatItem[];
    // cols is the widest layout; narrow screens always fall back to two.
    cols?: 2 | 3 | 4 | 5;
    // large is the headline treatment — bigger figures for a card whose whole
    // job is the numbers.
    large?: boolean;
    testid?: string;
  };
  let { items, cols = 4, large = false, testid }: Props = $props();

  const wide: Record<number, string> = {
    2: "sm:grid-cols-2",
    3: "sm:grid-cols-3",
    4: "sm:grid-cols-4",
    5: "sm:grid-cols-5",
  };
</script>

<dl class={`grid grid-cols-2 gap-x-6 gap-y-3 px-5 py-4 ${wide[cols]}`} data-testid={testid}>
  {#each items as it (it.label)}
    <div class="min-w-0">
      <dt class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">{it.label}</dt>
      <dd
        class={`mt-0.5 tabular-nums text-black-900 dark:text-white-100 ${large ? "text-xl font-semibold" : "truncate text-sm"}`}
        title={it.note ?? ""}
      >
        {it.value}
      </dd>
      {#if it.note}
        <p class="mt-0.5 text-[0.6875rem] leading-tight text-black-700 dark:text-black-600">{it.note}</p>
      {/if}
    </div>
  {/each}
</dl>
