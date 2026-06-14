<script lang="ts">
  import type { ProcessInfo } from "../types/agents.js";

  type Props = {
    processes: ProcessInfo[];
    onKill: (sessionId: string) => void;
    onDequeue: (sessionId: string) => void;
  };

  let { processes, onKill, onDequeue }: Props = $props();

  type RowData = {
    proc: ProcessInfo;
    pid: string;
    lc: string;
    lcCls: string;
    isQueued: boolean;
  };

  function lifecycleCls(lc: string): string {
    const map: Record<string, string> = {
      working: "bg-green-100 dark:bg-green-900 text-green-700 dark:text-green-300",
      idle: "bg-amber-100 dark:bg-amber-900 text-amber-700 dark:text-amber-300",
      spawning: "bg-blue-100 dark:bg-blue-900 text-blue-700 dark:text-blue-300",
      queued: "bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600",
      killed: "bg-red-100 dark:bg-red-900 text-red-700 dark:text-red-300",
      dead: "bg-red-100 dark:bg-red-900 text-red-700 dark:text-red-300",
    };
    return map[lc] ?? "bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600";
  }

  function buildRow(p: ProcessInfo): RowData {
    const dead = p.alive === false && p.lifecycle !== "queued";
    const lc = dead ? "dead" : (p.lifecycle || "—");
    return {
      proc: p,
      pid: p.pid > 0 ? String(p.pid) : "—",
      lc,
      lcCls: lifecycleCls(lc),
      isQueued: p.lifecycle === "queued",
    };
  }

  const rows = $derived(processes.map(buildRow));
</script>

<div class="flex-1 overflow-y-auto p-4 space-y-3">
  {#if rows.length === 0}
    <p class="text-xs text-black-700 dark:text-black-600 py-4 px-2">No active processes for this session.</p>
  {:else}
    {#each rows as row (row.proc.session_id)}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-3 space-y-2">
        <div class="flex items-center justify-between gap-2">
          <div class="flex items-center gap-2 min-w-0">
            <span class="text-xs font-semibold text-black-900 dark:text-white-100 truncate">{row.proc.agent_name || "—"}</span>
            <span class={"rounded px-1.5 py-0.5 text-[10px] font-medium " + row.lcCls}>{row.lc}</span>
          </div>
          {#if row.isQueued}
            <button
              type="button"
              onclick={() => onDequeue(row.proc.session_id)}
              class="kill-process-btn shrink-0 rounded px-2 py-1 text-[10px] font-medium bg-red-100 dark:bg-red-900 text-red-700 dark:text-red-300 hover:bg-red-200 dark:hover:bg-red-800 transition-colors"
            >Cancel</button>
          {:else}
            <button
              type="button"
              onclick={() => onKill(row.proc.session_id)}
              class="kill-process-btn shrink-0 rounded px-2 py-1 text-[10px] font-medium bg-red-100 dark:bg-red-900 text-red-700 dark:text-red-300 hover:bg-red-200 dark:hover:bg-red-800 transition-colors"
            >Kill</button>
          {/if}
        </div>
        <dl class="grid grid-cols-2 gap-x-3 gap-y-1 text-[11px]">
          <dt class="text-black-700 dark:text-black-600">Provider</dt>
          <dd class="font-mono text-black-900 dark:text-white-100">{row.proc.provider || "—"}</dd>
          <dt class="text-black-700 dark:text-black-600">PID</dt>
          <dd class="font-mono text-black-900 dark:text-white-100">{row.pid}</dd>
          <dt class="text-black-700 dark:text-black-600">Session</dt>
          <dd class="font-mono text-black-900 dark:text-white-100">{row.proc.session_id.slice(0, 8)}</dd>
          {#if row.proc.substate}
            <dt class="text-black-700 dark:text-black-600">Substate</dt>
            <dd class="font-mono text-black-900 dark:text-white-100">{row.proc.substate}</dd>
          {/if}
          {#if row.proc.queued > 0}
            <dt class="text-black-700 dark:text-black-600">Queued</dt>
            <dd class="font-mono text-amber-600 dark:text-amber-400">{row.proc.queued} waiting</dd>
          {/if}
        </dl>
      </div>
    {/each}
  {/if}
</div>
