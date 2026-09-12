<script lang="ts">
  /* Provenance chip for a usage reading: a cache glyph plus how long
     ago the numbers were actually fetched ("2m ago").

     It exists because the reading is NOT fetched per paint. The usage
     endpoint is rate-limited, so the server probes once per account,
     spaces probes apart and serves the result from cache — which means
     a bare percentage would imply a live call the page never made.
     The chip says what the number is: cached, and this old. */
  import { cacheHint } from "$lib/usagerings.js";

  type Props = { ageS: number; nextS?: number; fetchedAt?: string };
  let { ageS, nextS = 0, fetchedAt = "" }: Props = $props();

  let hint = $derived(cacheHint(ageS, nextS));
  /* The tooltip adds the absolute local time when the server sent one —
     useful when the age is minutes old and the reader wants the clock. */
  let title = $derived.by(() => {
    if (!hint.full) return "";
    if (!fetchedAt) return hint.full;
    const t = new Date(fetchedAt);
    if (Number.isNaN(t.getTime())) return hint.full;
    return `${hint.full} (at ${t.toLocaleTimeString()})`;
  });
</script>

{#if hint.short}
  <span
    data-testid="usage-cache-chip"
    class="inline-flex items-center gap-1 whitespace-nowrap text-black-600 dark:text-black-700"
    {title}
    aria-label={title}
  >
    <!-- Circular-arrow-over-disc: cached value that refreshes on a timer. -->
    <svg width="11" height="11" viewBox="0 0 12 12" fill="none" aria-hidden="true" class="shrink-0">
      <path
        d="M10 6a4 4 0 1 1-1.2-2.85"
        stroke="currentColor"
        stroke-width="1.2"
        stroke-linecap="round"
      />
      <path d="M10.4 1.6v2.2H8.2" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round" />
    </svg>
    {hint.short}
  </span>
{/if}
