<script lang="ts">
  import type { ThreadBlock, TurnEventPayload } from "../types/agents.js";
  import { subAgentStatusCls, subAgentStatusLabel } from "../lifecycleCls.js";

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
    // Opens the Sub-agents rail panel on this delegation's child. Only
    // meaningful for wick_delegate cards; omitted elsewhere.
    onOpenSubAgent?: (delegationId: string) => void;
    // Fetches a large (spilled) result payload by its trace event id when the
    // user expands the result — keeps the big blob out of the index load.
    loadEventPayload?: (eventId: string) => Promise<TurnEventPayload>;
  };
  let { block, onCancel, onDismiss, interrupted = false, onOpenSubAgent, loadEventPayload }: Props = $props();

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
  //
  // "Finished" means the result EVENT exists, not that result text is present:
  // a large (spilled) result has hasResult:true with no inline text until it
  // is fetched on demand. Live-built blocks don't carry hasResult — there,
  // text presence stays the signal.
  const hasResult = $derived(block.hasResult ?? block.result !== undefined);
  const running = $derived(!hasResult && !!block.startedAt && !interrupted);
  const wasInterrupted = $derived(!hasResult && interrupted);

  // Large (spilled) result payload, fetched on first expand so the big blob
  // never rides along with the trace index.
  let loadedResult = $state<string | null>(null);
  let loadedTruncated = $state(false);
  let resultLoading = $state(false);
  let resultLoadError = $state(false);

  const displayResult = $derived(block.result ?? loadedResult ?? undefined);
  const sizeLabel = $derived(
    block.resultSize ? `${(block.resultSize / 1024).toFixed(1)} KB` : ""
  );

  async function fetchLargeResult(): Promise<void> {
    if (resultLoading || loadedResult !== null || !loadEventPayload || !block.resultEventId) return;
    resultLoading = true;
    resultLoadError = false;
    try {
      const p = await loadEventPayload(block.resultEventId);
      loadedResult = p.text ?? "";
      loadedTruncated = p.truncated === true;
    } catch {
      resultLoadError = true;
    } finally {
      resultLoading = false;
    }
  }

  function toggleResult(): void {
    resultCollapsed = !resultCollapsed;
    if (!resultCollapsed && block.resultLarge && block.result === undefined) void fetchLargeResult();
  }

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

  // ── sub-agent delegation summary ────────────────────────────────
  // A wick_delegate call gets a one-line summary here instead of a raw
  // JSON dump: which role ran, how it ended, and the first line of what
  // it returned. The full transcript lives in the Sub-agents rail panel,
  // but this card stays in the thread as the audit record of "here is
  // where the leader delegated, and here is what came back".
  const isDelegate = $derived(block.toolName.replace(/^mcp__[^_]+__/, "") === "wick_delegate");

  const delegateInfo = $derived.by(() => {
    if (!isDelegate) return null;
    let profile = "";
    try {
      profile = JSON.parse(block.toolInput || "{}")?.profile ?? "";
    } catch { /* malformed input — fall back to the result payload */ }
    let status = "";
    let text = "";
    let delegationId = "";
    try {
      // displayResult so a spilled delegate result fills in once fetched.
      const r = JSON.parse(displayResult || "{}");
      status = r?.status ?? "";
      text = r?.result ?? "";
      delegationId = r?.delegation_id ?? "";
      if (!profile) profile = r?.profile ?? "";
    } catch { /* still running, or a plain-text error */ }
    return { profile, status, text, delegationId };
  });

  // First non-empty line of the sub-agent's answer, for the one-line preview.
  const delegateLine = $derived(
    (delegateInfo?.text ?? "").split("\n").map((l) => l.trim()).find((l) => l.length > 0) ?? "",
  );

  // Tools like Bash/Agent carry a human-readable `description` in their
  // input ("List files with sizes and count") — surface it in the header
  // so the collapsed card says WHY the tool ran, not just which tool.
  const inputDescription = $derived.by(() => {
    if (!block.toolInput) return "";
    try {
      const d = (JSON.parse(block.toolInput) as { description?: unknown }).description;
      return typeof d === "string" ? d.trim() : "";
    } catch {
      return "";
    }
  });

  // MCP-namespaced names (mcp__wick__wick_set_title) read as noise in the
  // header — show the bare tool name; the full name stays in the tooltip.
  const displayName = $derived(block.toolName.replace(/^mcp__.+?__/, ""));

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
    <span class="font-mono font-medium text-black-900 dark:text-white-100 shrink-0" title={block.toolName}>{displayName}</span>
    <!-- "Why it ran" hugs the name; flex-1 also works as the spacer that
         pushes the metadata cluster right when there's no description. -->
    <span data-tool-description class="flex-1 min-w-0 truncate text-black-700 dark:text-black-600">{inputDescription}</span>
    {#if duration || startLabel}
      <span class="font-mono text-[10px] text-black-500 dark:text-black-600 shrink-0">{duration}{duration && startLabel ? " · " : ""}{startLabel}</span>
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

  {#if delegateInfo}
    <div class="flex items-center gap-2 border-t border-white-300 dark:border-navy-600 px-3 py-2">
      {#if delegateInfo.profile}
        <span class="rounded-full bg-white-200 dark:bg-navy-700 px-2 py-0.5 text-[10px] font-medium text-black-900 dark:text-white-100 shrink-0"
          >{delegateInfo.profile}</span
        >
      {/if}
      {#if delegateInfo.status}
        <span class={"rounded px-1.5 py-0.5 text-[10px] font-medium shrink-0 " + subAgentStatusCls(delegateInfo.status)}
          >{subAgentStatusLabel(delegateInfo.status)}</span
        >
      {/if}
      {#if delegateLine}
        <span class="min-w-0 flex-1 truncate text-[11px] text-black-800 dark:text-black-600"
          >{delegateLine}</span
        >
      {/if}
      {#if onOpenSubAgent && delegateInfo.delegationId}
        <button
          type="button"
          onclick={(e) => { e.stopPropagation(); onOpenSubAgent(delegateInfo.delegationId); }}
          class="shrink-0 rounded px-2 py-1 text-[10px] font-medium text-link-400 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
        >Open</button>
      {/if}
    </div>
  {/if}

  {#if !inputCollapsed}
    <div class="border-t border-white-300 dark:border-navy-600">
      {#if prettyInput}
        <pre class="overflow-x-auto px-3 py-2 font-mono text-[11px] text-black-900 dark:text-white-100 leading-relaxed whitespace-pre-wrap break-words">{prettyInput}</pre>
      {:else}
        <p class="px-3 py-2 text-black-500 dark:text-black-600 italic">no input</p>
      {/if}
    </div>
  {/if}
  {#if hasResult}
    <div class="border-t border-white-300 dark:border-navy-600">
      <button
        type="button"
        onclick={toggleResult}
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
        {#if displayResult !== undefined}
          <span class="ml-2 truncate font-mono opacity-60">{displayResult.slice(0, 80).replace(/\n/g, " ")}{displayResult.length > 80 ? "…" : ""}</span>
        {:else}
          <!-- Spilled payload not fetched yet — show its size so the reader
               knows the result exists and what expanding will pull. -->
          <span class="ml-2 truncate opacity-60">{sizeLabel}{sizeLabel ? " — " : ""}click to load</span>
        {/if}
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
          {#if resultLoading}
            <p class="px-3 py-2 italic text-black-500 dark:text-black-600">loading…</p>
          {:else if resultLoadError}
            <div class="flex items-center gap-2 px-3 py-2 text-red-600 dark:text-red-400">
              <span>failed to load result</span>
              <button type="button" onclick={() => void fetchLargeResult()} class="underline hover:no-underline">retry</button>
            </div>
          {:else if displayResult !== undefined}
            {#if loadedTruncated}
              <p class="px-3 pt-2 text-[10px] italic text-amber-600 dark:text-amber-400">content truncated by the trace size cap</p>
            {/if}
            <pre class="overflow-x-auto px-3 py-2 font-mono text-[11px] text-black-900 dark:text-white-100 leading-relaxed whitespace-pre-wrap break-words">{displayResult}</pre>
          {:else}
            <!-- No fetcher wired (e.g. a synthetic turn) — nothing to show. -->
            <p class="px-3 py-2 italic text-black-500 dark:text-black-600">payload not loaded</p>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</div>
