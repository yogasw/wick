<script lang="ts">
  import { renderMarkdown } from "@wick-fe/common-md";
  import type { TraceContext, TraceDisplay } from "../types.js";
  type Props = { display: TraceDisplay; ctx?: TraceContext };
  let { display }: Props = $props();
  // renderMarkdown escapes the source before formatting, same as chat text.
  const html = $derived(renderMarkdown(display.body ?? ""));
</script>

<!-- Same renderer (and so the same heading/list/table classes) as chat
     text, but in the UI face: .wick-prose is the serif 16px reading style
     for agent replies, and it only loads in bundles that import the chat
     renderer — a tool's output inside a trace card is neither. -->
<div data-trace-kind="markdown" class="trace-md markdown-body px-3 py-2 text-xs text-black-900 dark:text-white-100 break-words">{@html html}</div>

<style>
  .trace-md {
    font-family: InterVariable, Inter, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    line-height: 1.5;
  }
  .trace-md :global(> * + *) {
    margin-top: 0.5em;
  }
  .trace-md :global(p) {
    margin: 0;
  }
  .trace-md :global(h1:first-child),
  .trace-md :global(h2:first-child),
  .trace-md :global(h3:first-child),
  .trace-md :global(h4:first-child) {
    margin-top: 0;
  }
  .trace-md :global(code),
  .trace-md :global(pre) {
    font-family: Menlo, Consolas, Monaco, monospace;
  }
</style>
