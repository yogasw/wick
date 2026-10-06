<script lang="ts">
  import JsonBlock from "./JsonBlock.svelte";
  import TextBlock from "./TextBlock.svelte";
  import type { TraceContext, TraceDisplay } from "../types.js";
  type Props = { display: TraceDisplay; ctx?: TraceContext };
  let { display }: Props = $props();
  const isJson = $derived.by(() => { try { JSON.parse(display.body ?? ""); return true; } catch { return false; } });
</script>

<div data-trace-kind="mcp">
  {#if display.connector || display.op}
    <div class="flex flex-wrap items-center gap-1.5 px-3 pt-2 text-[11px]">
      {#if display.connector}<span data-connector class="rounded bg-white-200 dark:bg-navy-700 px-1.5 py-0.5 font-mono text-black-900 dark:text-white-100">{display.connector}</span>{/if}
      {#if display.op}<span data-op class="font-mono font-medium text-black-900 dark:text-white-100">{display.op}</span>{/if}
    </div>
  {/if}
  {#if display.body}
    {#if isJson}<JsonBlock {display} />{:else}<TextBlock {display} />{/if}
  {/if}
</div>
