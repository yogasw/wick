<script lang="ts">
  import type { TraceContext, TraceDisplay } from "../types.js";
  type Props = { display: TraceDisplay; ctx?: TraceContext };
  let { display }: Props = $props();
</script>

<pre data-trace-kind="text" class="px-3 py-2 font-mono text-[11px] leading-relaxed whitespace-pre-wrap break-words text-black-900 dark:text-white-100">{display.body ?? ""}</pre>
