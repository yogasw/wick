<script lang="ts">
  import type { TraceContext, TraceDisplay } from "../types.js";
  type Props = { display: TraceDisplay; ctx?: TraceContext };
  let { display }: Props = $props();
  const failed = $derived(display.exit_code !== undefined && display.exit_code !== 0);
</script>

<div data-trace-kind="terminal" class="bg-navy-800 text-white-100">
  {#if display.exit_code !== undefined}
    <div class="flex justify-end px-3 pt-1.5">
      <span data-exit-code class="rounded px-1.5 py-0.5 font-mono text-[10px] font-medium {failed ? 'bg-red-600 text-white-100' : 'bg-green-600 text-white-100'}">exit {display.exit_code}</span>
    </div>
  {/if}
  <pre class="px-3 py-2 font-mono text-[11px] leading-relaxed whitespace-pre-wrap break-words">{display.body || "(no output)"}</pre>
</div>
