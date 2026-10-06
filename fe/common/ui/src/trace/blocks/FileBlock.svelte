<script lang="ts">
  import CodeBlock from "./CodeBlock.svelte";
  import CopyButton from "./CopyButton.svelte";
  import type { TraceContext, TraceDisplay } from "../types.js";
  type Props = { display: TraceDisplay; ctx?: TraceContext };
  let { display }: Props = $props();
</script>

<div data-trace-kind="file">
  {#if display.path}
    <div class="flex items-center gap-2 px-3 pt-2">
      <span data-path class="min-w-0 flex-1 break-all font-mono text-[11px] text-black-800 dark:text-black-600">{display.path}</span>
      <CopyButton text={display.path} label="Copy path" />
    </div>
  {/if}
  {#if display.body}
    <CodeBlock {display} />
  {:else if display.summary && display.summary !== display.path}
    <p class="px-3 pb-2 font-mono text-[11px] text-black-600 dark:text-black-500">{display.summary}</p>
  {/if}
</div>
