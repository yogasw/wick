<script lang="ts">
  import type { TraceContext, TraceDisplay } from "../types.js";
  import { getTraceHighlighter } from "../highlight.js";
  type Props = { display: TraceDisplay; ctx?: TraceContext; code?: string; lang?: string };
  let { display, code, lang }: Props = $props();

  const src = $derived(code ?? display.body ?? "");
  const language = $derived(lang ?? display.lang ?? "");
  let highlighted = $state<string | null>(null);

  $effect(() => {
    const s = src, l = language;
    highlighted = null;
    const hl = getTraceHighlighter();
    // Highlighting a huge payload freezes the tab for no reading gain.
    if (!hl || !l || s.length > 200_000) return;
    let live = true;
    hl(s, l).then((h) => { if (live) highlighted = h; }).catch(() => {});
    return () => { live = false; };
  });
</script>

<pre data-trace-kind="code" data-lang={language} class="px-3 py-2 font-mono text-[11px] leading-relaxed whitespace-pre-wrap break-words text-black-900 dark:text-white-100"><code class={language ? `hljs language-${language}` : ""}>{#if highlighted !== null}{@html highlighted}{:else}{src}{/if}</code></pre>
