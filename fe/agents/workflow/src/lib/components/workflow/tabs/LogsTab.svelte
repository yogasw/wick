<script lang="ts">
  // Logs tab — wires to the live SSE stream populated by
  // stores/sse.ts. Mirrors the legacy editor's STARTED / COMPLETED
  // rows with per-row "copy" button + duration when present.
  import { logLines, type LogLine } from "$lib/stores/sse";

  function copyLine(line: LogLine) {
    const text = `${line.ts} [${line.status}] ${line.event}${line.node ? " " + line.node : ""}${line.case ? " case=" + line.case : ""}`;
    navigator.clipboard?.writeText(text);
  }

  const statusColor = (s: LogLine["status"]) =>
    s === "completed" ? "bg-emerald-500/20 text-emerald-300"
    : s === "started" ? "bg-amber-500/20 text-amber-300"
    : s === "failed" ? "bg-rose-500/20 text-rose-300"
    : s === "skipped" ? "bg-slate-500/20 text-slate-400"
    : "bg-slate-700/30 text-slate-300";

  // Optional `lines` prop kept for compatibility with the older
  // placeholder caller; live store wins when populated.
  type Props = { lines?: LogLine[] };
  let { lines = [] }: Props = $props();
  const rows = $derived($logLines.length > 0 ? $logLines : lines);
</script>

{#if rows.length === 0}
  <p class="text-xs text-slate-500">No log lines yet. Hit <em>Execute workflow</em> to fire a run.</p>
{:else}
  <ul class="divide-y divide-slate-200 dark:divide-slate-700 font-mono text-[11px]">
    {#each rows as line}
      <li class="flex items-center gap-3 py-1.5">
        <span class={`px-2 py-0.5 rounded text-[10px] uppercase tracking-wider ${statusColor(line.status)}`}>{line.status}</span>
        <span class="text-slate-500">{line.ts.slice(11, 23)}</span>
        <span class="flex-1 truncate">
          {line.event}
          {#if line.node}<span class="text-slate-400 ml-1">{line.node}</span>{/if}
          {#if line.case}<span class="ml-1 px-1 rounded bg-slate-700 text-[10px]">case={line.case}</span>{/if}
        </span>
        <button class="text-slate-400 hover:text-slate-100 text-[10px]" onclick={() => copyLine(line)}>copy</button>
      </li>
    {/each}
  </ul>
{/if}
