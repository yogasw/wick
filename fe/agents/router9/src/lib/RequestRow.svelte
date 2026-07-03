<script lang="ts">
  import type { ReqRow } from "./types.js";
  import { prettyJSON } from "./json.js";

  type Props = {
    row: ReqRow;
    /** Open the fullscreen JSON analysis modal for a body. */
    onAnalyze: (title: string, raw: string) => void;
  };
  let { row, onAnalyze }: Props = $props();

  let expanded = $state(false);

  function statusClass(st: number): string {
    if (!st) return "text-black-700 dark:text-black-600";
    if (st >= 500) return "text-red-600 dark:text-red-400";
    if (st >= 400) return "text-amber-600 dark:text-amber-400";
    return "text-green-600 dark:text-green-300";
  }
</script>

<div class="rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-2">
  <!-- Summary line -->
  <button type="button" onclick={() => (expanded = !expanded)} class="flex w-full flex-wrap items-center gap-x-3 gap-y-1 text-left text-[0.8125rem]">
    <span class="font-mono text-black-700 dark:text-black-600">{row.time}</span>
    <span class={`font-semibold ${statusClass(row.status)}`}>{row.status || "—"}</span>
    <span class="font-mono font-medium text-black-900 dark:text-white-100">{row.method} {row.path}</span>
    {#if row.model}<span class="font-mono text-[0.75rem] text-black-800 dark:text-black-600">{row.model}</span>{/if}
    <span class="ml-auto text-[0.75rem] text-black-700 dark:text-black-600">{row.duration_ms || 0}ms</span>
    <svg class={`h-3.5 w-3.5 text-black-600 transition-transform ${expanded ? "rotate-90" : ""}`} viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"></path></svg>
  </button>

  <!-- Meta line -->
  <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1">
    {#if row.external}
      <span class="rounded bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-400 px-1.5 py-0.5 text-[0.6875rem] font-medium">external</span>
    {:else}
      <span class="rounded bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600 px-1.5 py-0.5 text-[0.6875rem] font-medium">local</span>
    {/if}
    <span class="text-[0.75rem] text-black-700 dark:text-black-600">host {row.host}</span>
    <span class="text-[0.75rem] text-black-700 dark:text-black-600">from {row.client_ip || row.remote_addr}</span>
    {#if row.auth}
      <span class="font-mono text-[0.75rem] text-black-700 dark:text-black-600">key {row.auth}</span>
    {:else}
      <span class="text-[0.75rem] text-black-700 dark:text-black-600">no key</span>
    {/if}
    {#if row.user_agent}
      <span class="truncate max-w-[16rem] text-[0.75rem] text-black-600" title={row.user_agent}>{row.user_agent}</span>
    {/if}
  </div>

  <!-- Expanded bodies -->
  {#if expanded}
    <div class="mt-2 space-y-2">
      {#each [["req", row.req_body], ["resp", row.resp_body]] as [label, body] (label)}
        {#if body}
          <div>
            <div class="mb-0.5 flex items-center justify-between">
              <span class="text-[0.6875rem] uppercase tracking-wide text-black-600">{label}</span>
              <button type="button" onclick={() => onAnalyze(`${row.method} ${row.path} — ${label}`, body)} class="rounded border border-white-400 dark:border-navy-600 px-1.5 py-0.5 text-[0.6875rem] text-black-700 dark:text-black-600 hover:border-black-700">Analyze ⤢</button>
            </div>
            <pre class="m-0 max-h-64 overflow-auto rounded bg-black/60 px-2 py-1 text-[0.7rem] leading-relaxed {label === 'req' ? 'text-green-300' : 'text-black-500'} whitespace-pre-wrap break-words">{prettyJSON(body)}</pre>
          </div>
        {/if}
      {/each}
      {#if !row.req_body && !row.resp_body}
        <p class="text-[0.75rem] text-black-600">No body captured.</p>
      {/if}
    </div>
  {/if}
</div>
