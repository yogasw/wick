<script lang="ts">
  import { renderMarkdown } from "@wick-fe/common-md";
  import type { TraceContext, TraceDisplay } from "../types.js";
  type Props = { display: TraceDisplay; ctx?: TraceContext };
  let { display }: Props = $props();
  // renderMarkdown escapes the source before formatting, same as chat text.
  const html = $derived(renderMarkdown(display.body ?? ""));
</script>

<div data-trace-kind="markdown" class="wick-prose markdown-body px-3 py-2 text-xs text-black-900 dark:text-white-100 break-words">{@html html}</div>
