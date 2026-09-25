<script lang="ts">
  /* One setting: its name, the sentence that says what it costs, and the
     control.

     The consequence is a first-class slot rather than a helper hint because
     that is what this whole tab is for — most of these knobs are invisible
     when you flip them and obvious a month later. `danger` reddens the note
     for the ones where the cost is irreversible or leaves the host. */
  import type { Snippet } from "svelte";

  type Props = {
    label: string;
    note?: string;
    danger?: boolean;
    id?: string;
    children: Snippet;
  };
  let { label, note, danger = false, id, children }: Props = $props();
</script>

<div class="flex flex-col gap-2 px-5 py-4 sm:flex-row sm:items-start sm:justify-between sm:gap-6">
  <div class="min-w-0 sm:max-w-md">
    <p class="text-xs font-medium text-black-900 dark:text-white-100">{label}</p>
    {#if note}
      <p
        {id}
        class={`mt-1 text-[0.6875rem] leading-relaxed ${
          danger ? "text-cau-400 dark:text-cau-400" : "text-black-700 dark:text-black-600"
        }`}
      >
        {note}
      </p>
    {/if}
  </div>
  <div class="w-full shrink-0 sm:w-64">{@render children()}</div>
</div>
