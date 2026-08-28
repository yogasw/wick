<script lang="ts">
  /* End-of-list detector shared by every scrollable list (sessions,
     untracked rail, ticket-card rows): render it as the LAST child of the
     scroll container, and when it comes into view it calls onMore —
     revealing the next chunk or fetching the next server page — until
     canMore turns false. jsdom and old browsers have no
     IntersectionObserver; they get a "Show more" button doing the same
     step. */
  type Props = {
    canMore: boolean;
    loading?: boolean;
    /* Any value that changes when a chunk lands (a length works): the
       advance effect keys on it, so a sentinel that STAYS in view keeps
       stepping until the list outgrows the viewport or canMore ends. */
    epoch?: number;
    onMore: () => void;
  };

  let { canMore, loading = false, epoch = 0, onMore }: Props = $props();

  let el = $state<HTMLElement | null>(null);
  let inView = $state(false);
  const ioSupported = typeof IntersectionObserver !== "undefined";

  $effect(() => {
    if (!ioSupported || !el) return;
    const io = new IntersectionObserver((entries) => {
      inView = entries.some((e) => e.isIntersecting);
    });
    io.observe(el);
    return () => io.disconnect();
  });

  $effect(() => {
    epoch;
    if (inView && canMore && !loading) onMore();
  });
</script>

{#if canMore}
  <div bind:this={el} data-testid="scroll-sentinel" class="h-px"></div>
  {#if loading}
    <p class="py-1 text-center text-xs text-black-600 dark:text-black-700">Loading…</p>
  {:else if !ioSupported}
    <div class="flex justify-center pt-1">
      <button
        type="button"
        onclick={(e) => { e.stopPropagation(); onMore(); }}
        class="text-xs text-green-600 dark:text-green-400 hover:underline"
      >Show more</button>
    </div>
  {/if}
{/if}
