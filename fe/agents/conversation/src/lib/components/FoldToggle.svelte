<script lang="ts">
  /* The small "Show more / Show less" at the END of a folded section — at
     the end because that is where the reader runs out of text, and small
     because a full-width row costs a line of the panel for one word. */
  type Props = {
    open: boolean;
    /* How many items the fold hides, when it hides a countable list. */
    hidden?: number;
    /* Replaces "Show more" while closed, e.g. "Add custom fields". */
    label?: string;
    testid?: string;
    onclick: () => void;
  };
  let { open, hidden, label, testid, onclick }: Props = $props();
  const text = $derived(
    open ? "Show less" : `${label ?? "Show more"}${hidden ? ` (${hidden})` : ""}`,
  );
</script>

<div class="mt-1 flex justify-end">
  <button
    type="button"
    data-testid={testid}
    aria-expanded={open}
    {onclick}
    class="inline-flex items-center gap-0.5 rounded px-1 py-0.5 text-[11px] font-medium text-green-600 transition-colors hover:bg-white-200 dark:text-green-400 dark:hover:bg-navy-600"
  >
    {text}
    <svg viewBox="0 0 16 16" class="h-3 w-3 transition-transform {open ? 'rotate-180' : ''}" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
      <path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"></path>
    </svg>
  </button>
</div>
