<script lang="ts">
  /* ✦N — how many saved rate-limit resets the account holds. Hidden when
     there are none (or the provider reports none); amber when the soonest
     one lapses within SAVED_RESET_SOON_DAYS. Read-only: spending a reset
     is done from the CLI. */
  import { expiresSoon, savedResetsTooltip, showSavedResetsChip, type SavedResets } from "./base/index.js";

  type Props = { resets: SavedResets | null | undefined; class?: string };
  let { resets, class: klass = "" }: Props = $props();

  let soon = $derived(expiresSoon(resets));
  let tip = $derived(savedResetsTooltip(resets));
</script>

{#if showSavedResetsChip(resets)}
  <span
    data-testid="saved-resets-chip"
    title={tip}
    aria-label={tip}
    class="inline-flex shrink-0 items-center whitespace-nowrap rounded-full px-1.5 py-0.5 text-[11px] font-semibold leading-none {soon
      ? 'bg-cau-400/15 text-cau-400'
      : 'bg-link-400/15 text-link-400'} {klass}"
  >✦{resets?.available}</span>
{/if}
