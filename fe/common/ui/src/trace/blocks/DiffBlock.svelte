<script lang="ts">
  import type { TraceContext, TraceDisplay } from "../types.js";
  type Props = { display: TraceDisplay; ctx?: TraceContext };
  let { display }: Props = $props();

  // A truncated diff keeps whole hunks only: a hunk cut mid-way reads as
  // deleted/added lines that are not really there.
  const lines = $derived.by(() => {
    let ls = (display.body ?? "").replace(/\n$/, "").split("\n");
    if (display.truncated) {
      const last = ls.map((l, i) => (l.startsWith("@@") ? i : -1)).filter((i) => i > 0).pop();
      if (last !== undefined) ls = ls.slice(0, last);
    }
    return ls;
  });

  function cls(l: string): string {
    if (l.startsWith("+++") || l.startsWith("---") || l.startsWith("diff ") || l.startsWith("***")) return "text-black-600 dark:text-black-500 font-semibold";
    if (l.startsWith("@@")) return "text-sky-700 dark:text-sky-400";
    if (l.startsWith("+")) return "bg-emerald-50 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300";
    if (l.startsWith("-")) return "bg-red-50 text-red-800 dark:bg-red-900/30 dark:text-red-300";
    return "text-black-900 dark:text-white-100";
  }
</script>

{#if display.path}
  <div class="px-3 pt-2 font-mono text-[11px] text-black-600 dark:text-black-500 break-all">{display.path}</div>
{/if}
<pre data-trace-kind="diff" class="py-2 font-mono text-[11px] leading-relaxed whitespace-pre-wrap break-words">{#each lines as l, i (i)}<div class="px-3 {cls(l)}">{l || " "}</div>{/each}</pre>
