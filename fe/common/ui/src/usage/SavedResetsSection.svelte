<script lang="ts">
  /* The "SAVED RESETS" block of a usage panel: count, one row per reset
     with its "use by" date, and whether one can be spent now. Renders
     nothing when the reading is absent (unsupported type, or the read
     failed) — the usage above it is unaffected. */
  import {
    fmtResetDate,
    fmtResetDateTime,
    inCooldown,
    savedResetsEmptyText,
    usableNowText,
    type SavedResets,
  } from "./base/index.js";

  type Props = { resets: SavedResets | null | undefined };
  let { resets }: Props = $props();

  let now = $derived(Date.now());
  let cooling = $derived(inCooldown(resets, now));
  let usable = $derived(usableNowText(resets, now));
  let count = $derived(
    resets && resets.total > resets.available && resets.total > 0
      ? `${resets.available} of ${resets.total}`
      : String(resets?.available ?? 0),
  );
</script>

{#if resets}
  <div data-testid="saved-resets-section" class="space-y-1.5">
    <div class="text-[11px] font-bold uppercase tracking-wide text-black-800 dark:text-black-600">Saved resets</div>
    {#if resets.available > 0}
      <div class="flex justify-between text-xs">
        <span class="text-black-800 dark:text-black-600">Available</span>
        <span class="font-mono text-slate-800 dark:text-white-100">{count}</span>
      </div>
      {#if resets.items.length > 0}
        <ul class="divide-y divide-white-300 rounded-md border border-white-300 dark:divide-navy-600 dark:border-navy-600">
          {#each resets.items as it, i (it.id || i)}
            <li class="flex justify-between gap-3 px-2 py-1 text-xs">
              <span class="truncate text-slate-800 dark:text-white-100">{it.label || "Saved reset"}</span>
              {#if it.expiresAt}
                <span class="shrink-0 text-black-800 dark:text-black-600">use by {fmtResetDate(it.expiresAt)}</span>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
      {#if cooling}
        <div class="text-xs text-cau-400">Next reset usable after {fmtResetDateTime(resets.cooldownUntil)}.</div>
      {:else if usable}
        <div class="flex justify-between text-xs">
          <span class="text-black-800 dark:text-black-600">Usable now</span>
          <span class="text-slate-800 dark:text-white-100">{usable}</span>
        </div>
      {/if}
      {#if resets.hint}
        <div class="text-[11px] text-black-800 dark:text-black-600">{resets.hint}</div>
      {/if}
    {:else}
      <div class="text-xs text-black-800 dark:text-black-600">{savedResetsEmptyText(resets)}</div>
    {/if}
  </div>
{/if}
