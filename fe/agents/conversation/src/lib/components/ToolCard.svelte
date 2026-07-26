<script lang="ts">
  import type { ThreadBlock } from "../types/agents.js";

  type ToolBlock = Extract<ThreadBlock, { kind: "tool" }>;

  // onCancel aborts the underlying connector run (per-operation — the agent turn
  // keeps going and receives a "cancelled" result). onDismiss just removes a
  // stuck card from the view without touching the backend (used for orphan runs
  // from before this feature, which carry no runId to cancel).
  type Props = {
    block: ToolBlock;
    onCancel?: (runId: string) => void;
    onDismiss?: (toolUseId: string) => void;
    // Set when this card belongs to an interrupted/cut-off turn (history path).
    // Such a tool call has no result but is NOT live — show "interrupted", not a
    // running spinner that never resolves.
    interrupted?: boolean;
  };
  let { block, onCancel, onDismiss, interrupted = false }: Props = $props();

  let cancelling = $state(false);

  function cancel(e: Event): void {
    e.stopPropagation();
    if (cancelling) return;
    if (block.runId && onCancel) {
      cancelling = true;
      onCancel(block.runId);
    } else if (onDismiss) {
      // No runId — an orphan/stale card (e.g. from before per-run cancel existed,
      // or a run whose finish event was lost). Nothing to abort server-side; just
      // clear it from the view.
      onDismiss(block.toolUseId);
    }
  }

  let inputCollapsed = $state(true);
  let resultCollapsed = $state(true);

  // A tool is still running until its result arrives. While running we show
  // a live elapsed timer so a long call (e.g. a shell command that takes
  // minutes) reads as "working", not stuck — the whole point of this card
  // during the 10m shell window the user hit. A tool call on an interrupted
  // turn is NOT running — its result never came because the turn was cut off,
  // so it must read "interrupted", never an eternal spinner.
  const running = $derived(block.result === undefined && !!block.startedAt && !interrupted);
  const wasInterrupted = $derived(block.result === undefined && interrupted);

  // The ✕ is shown on any running tool. It cancels the op when a runId is known
  // (a live run), otherwise it just dismisses the stuck card from the UI.
  const canCancel = $derived(running && (!!onCancel || !!onDismiss));

  // now ticks once a second ONLY while something on the page is running, so
  // the running timer updates without a permanent interval.
  let now = $state(Date.now());
  $effect(() => {
    if (!running) return;
    const t = setInterval(() => (now = Date.now()), 1000);
    return () => clearInterval(t);
  });

  function fmtSecs(s: number): string {
    if (s < 60) return `${s}s`;
    return `${Math.floor(s / 60)}m ${s % 60}s`;
  }

  function fmtDuration(startMs?: number, endMs?: number): string {
    if (!startMs || !endMs || endMs <= startMs) return "";
    return fmtSecs(Math.round((endMs - startMs) / 1000));
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
  // Live elapsed while running (now − startedAt), formatted the same way.
  const runningElapsed = $derived(
    running && block.startedAt ? fmtSecs(Math.max(0, Math.round((now - block.startedAt) / 1000))) : ""
  );
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
    {#if running}
      <span class="ml-auto flex items-center gap-1.5 text-[10px] font-medium text-green-600 dark:text-green-400 shrink-0">
        <svg viewBox="0 0 16 16" class="h-3 w-3 animate-spin" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M8 2a6 6 0 1 1-6 6" stroke-linecap="round"></path>
        </svg>
        running{#if runningElapsed} {runningElapsed}{/if}…
      </span>
      {#if canCancel}
        <span
          role="button"
          tabindex="0"
          title={block.runId ? "Cancel this operation (the agent keeps going)" : "Dismiss this stuck card"}
          aria-label={block.runId ? "Cancel this operation" : "Dismiss this stuck card"}
          aria-disabled={cancelling}
          onclick={cancel}
          onkeydown={(e) => { if (e.key === "Enter" || e.key === " ") cancel(e); }}
          class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-red-500 hover:bg-red-100 dark:hover:bg-red-900/30 {cancelling ? 'opacity-40' : ''}"
        >✕</span>
      {/if}
    {:else if wasInterrupted}
      <span class="ml-auto text-[10px] font-medium text-amber-700 dark:text-amber-300 uppercase tracking-wide shrink-0">interrupted</span>
    {:else}
      <span class="ml-auto text-[10px] text-black-500 dark:text-black-600 uppercase tracking-wide shrink-0">tool call</span>
    {/if}
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
