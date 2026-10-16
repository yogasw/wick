<script lang="ts">
  /* "working · waiting on N teammates": the chat's agent handed work to
     teammates that are still on it. Shared by the conversation header and
     the Team app's own header, so both read the same. 0 renders nothing. */
  import { waitingHeaderLabel } from "../../teammatesBusy.js";

  let { count }: { count: number } = $props();
</script>

{#if count > 0}
  <span
    data-teammate-badge
    title="Teammates are still working on what this conversation sent them"
    class="inline-flex shrink-0 items-center gap-1 rounded-full border border-navy-400 bg-white-200 dark:bg-navy-700 px-2 py-0.5 text-[10px] font-medium text-black-800 dark:text-white-300"
  >
    <span class="h-2 w-2 shrink-0 rounded-full border-2 border-navy-400 border-t-transparent animate-spin motion-reduce:animate-none" aria-hidden="true"></span>
    {waitingHeaderLabel(count)}
  </span>
{/if}
