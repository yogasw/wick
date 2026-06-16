<script lang="ts">
  import type { ThreadBlock } from "../types/agents.js";

  type ToolBlock = Extract<ThreadBlock, { kind: "tool" }>;

  type Props = { block: ToolBlock };
  let { block }: Props = $props();

  let inputCollapsed = $state(true);
  let resultCollapsed = $state(true);

  function fmtDuration(startMs?: number, endMs?: number): string {
    if (!startMs || !endMs || endMs <= startMs) return "";
    const s = Math.round((endMs - startMs) / 1000);
    if (s < 60) return `${s}s`;
    return `${Math.floor(s / 60)}m ${s % 60}s`;
  }

  function fmtTime(ms?: number): string {
    if (!ms) return "";
    return new Date(ms).toTimeString().slice(0, 8);
  }

  const prettyInput = $derived(
    block.toolInput
      ? (() => { try { return JSON.stringify(JSON.parse(block.toolInput), null, 2); } catch { return block.toolInput; } })()
      : ""
  );

  const duration = $derived(fmtDuration(block.startedAt, block.endedAt));
  const startLabel = $derived(fmtTime(block.startedAt));
</script>

<div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs">
  <button
    type="button"
    onclick={() => (inputCollapsed = !inputCollapsed)}
    class="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
  >
    <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-green-600 dark:text-green-400" fill="none" stroke="currentColor" stroke-width="1.5">
      <path d="M2 4h4v8H2zM10 4h4v8h-4z" stroke-linejoin="round"></path>
      <path d="M6 8h4" stroke-linecap="round"></path>
    </svg>
    <span class="font-mono font-medium text-black-900 dark:text-white-100">{block.toolName}</span>
    {#if startLabel}
      <span class="font-mono text-[10px] text-black-500 dark:text-black-600">{startLabel}</span>
    {/if}
    {#if duration}
      <span class="font-mono text-[10px] text-black-500 dark:text-black-600">· {duration}</span>
    {/if}
    <span class="ml-auto text-[10px] text-black-500 dark:text-black-600 uppercase tracking-wide shrink-0">tool call</span>
    <svg
      data-chevron
      viewBox="0 0 16 16"
      class="h-3 w-3 shrink-0 text-black-500 transition-transform"
      style={inputCollapsed ? "transform:rotate(-90deg)" : ""}
      fill="none"
      stroke="currentColor"
      stroke-width="1.5"
    >
      <path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"></path>
    </svg>
  </button>
  {#if !inputCollapsed}
    <div class="border-t border-white-300 dark:border-navy-600">
      {#if prettyInput}
        <pre class="overflow-x-auto px-3 py-2 font-mono text-[11px] text-black-900 dark:text-white-100 leading-relaxed whitespace-pre-wrap break-words">{prettyInput}</pre>
      {:else}
        <p class="px-3 py-2 text-black-500 dark:text-black-600 italic">no input</p>
      {/if}
    </div>
  {/if}
  {#if block.result !== undefined}
    <div class="border-t border-white-300 dark:border-navy-600">
      <button
        type="button"
        onclick={() => (resultCollapsed = !resultCollapsed)}
        class={`flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-white-200 dark:hover:bg-navy-800 transition-colors ${block.isError ? "text-red-600 dark:text-red-400" : "text-black-600 dark:text-black-700"}`}
      >
        <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5">
          {#if block.isError}
            <circle cx="8" cy="8" r="5.5"></circle>
            <path d="M8 5v4M8 11v.5" stroke-linecap="round"></path>
          {:else}
            <path d="M3 8l3 3 7-7" stroke-linecap="round" stroke-linejoin="round"></path>
          {/if}
        </svg>
        <span class="text-[10px] uppercase tracking-wide shrink-0">{block.isError ? "error" : "result"}</span>
        <span class="ml-2 truncate font-mono opacity-60">{block.result.slice(0, 80).replace(/\n/g, " ")}{block.result.length > 80 ? "…" : ""}</span>
        <svg
          data-chevron
          viewBox="0 0 16 16"
          class="ml-auto h-3 w-3 shrink-0 text-black-500 transition-transform"
          style={resultCollapsed ? "transform:rotate(-90deg)" : ""}
          fill="none"
          stroke="currentColor"
          stroke-width="1.5"
        >
          <path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
      </button>
      {#if !resultCollapsed}
        <div class="border-t border-white-300 dark:border-navy-600">
          <pre class="overflow-x-auto px-3 py-2 font-mono text-[11px] text-black-900 dark:text-white-100 leading-relaxed whitespace-pre-wrap break-words">{block.result}</pre>
        </div>
      {/if}
    </div>
  {/if}
</div>
