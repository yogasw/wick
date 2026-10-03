<script lang="ts">
  import type { TraceContext, TraceDisplay } from "../types.js";
  type Props = { display: TraceDisplay; ctx?: TraceContext };
  let { display }: Props = $props();
  // A call card carries pattern/path; a result carries the matched list.
  const isCall = $derived(!!display.pattern);
  const hits = $derived(isCall ? [] : (display.body ?? "").split("\n").filter((l) => l.trim() !== ""));
</script>

<div data-trace-kind="search" class="px-3 py-2 font-mono text-[11px]">
  {#if isCall}
    <div><span class="text-black-600 dark:text-black-500">pattern </span><span class="text-emerald-700 dark:text-emerald-400 break-all">{display.pattern}</span></div>
    {#if display.path}<div><span class="text-black-600 dark:text-black-500">in </span><span class="break-all text-black-900 dark:text-white-100">{display.path}</span></div>{/if}
  {:else}
    <div class="mb-1 text-[10px] text-black-600 dark:text-black-500">{hits.length} result{hits.length === 1 ? "" : "s"}</div>
    {#each hits as h, i (i)}<div class="break-all text-black-900 dark:text-white-100">{h}</div>{/each}
  {/if}
</div>
