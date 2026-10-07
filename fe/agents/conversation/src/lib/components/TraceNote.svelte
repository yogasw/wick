<script lang="ts">
  import { tidyTraceNote } from "../traceNote.js";

  // One look for every note in a trace. `kind` only picks the data attribute
  // tests and styles key on; it does not change how the note looks — thinking
  // and narration are the same thing to someone reading the trace, and the
  // provider decides which one a sentence arrives as.
  let { kind, text }: { kind: "thinking" | "text"; text: string } = $props();

  const shown = $derived(tidyTraceNote(text));
</script>

{#if shown}
  <div
    data-thinking-block={kind === "thinking" ? "" : undefined}
    data-text-block={kind === "text" ? "" : undefined}
    class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs px-3 py-2 italic text-black-600 dark:text-black-700 whitespace-pre-wrap break-words"
  >{shown}</div>
{/if}
