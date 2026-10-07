<script lang="ts">
  import CodeBlock from "./CodeBlock.svelte";
  import CopyButton from "./CopyButton.svelte";
  import type { TraceContext, TraceDisplay } from "../types.js";
  type Props = { display: TraceDisplay; ctx?: TraceContext };
  let { display }: Props = $props();
  const cmd = $derived(display.command ?? display.body ?? "");
  const timeout = $derived(
    display.timeout ? (display.timeout >= 1000 ? `${Math.round(display.timeout / 1000)}s` : `${display.timeout}ms`) : "",
  );
</script>

<div data-trace-kind="command">
  {#if display.cwd || timeout}
    <div class="flex flex-wrap gap-x-3 px-3 pt-2 font-mono text-[10px] text-black-600 dark:text-black-500">
      {#if display.cwd}<span data-cwd>cwd {display.cwd}</span>{/if}
      {#if timeout}<span>timeout {timeout}</span>{/if}
    </div>
  {/if}
  <div class="relative">
    <CodeBlock {display} code={"$ " + cmd} lang="shell" />
    <div class="absolute right-1 top-1"><CopyButton text={cmd} label="Copy command" /></div>
  </div>
</div>
