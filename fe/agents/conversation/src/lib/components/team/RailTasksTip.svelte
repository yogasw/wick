<script lang="ts">
  /* The Tasks tab's icons-mode tooltip, as the mockup's:
     "<b>Tasks</b> · 2 active · <em>1 needs input</em>", the waiting part in
     the warn colour. Same words as the tab's aria-label (railTabName). */
  let { label, count, needsInput }: { label: string; count: number; needsInput: number } = $props();
</script>

<b class="font-semibold">{label}</b>{#if count > 0}{" · "}{count} active{/if}{#if needsInput > 0}{" · "}<em
    data-testid="rail-tip-tasks-needs"
    class="not-italic text-amber-700 dark:text-amber-300">{needsInput} needs input</em>{/if}
