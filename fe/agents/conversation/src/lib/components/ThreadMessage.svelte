<script lang="ts">
  import type { ConversationTurn, ThreadBlock, TurnEvent } from "../types/agents.js";
  import { renderMarkdown, linkifyText } from "../markdown.js";
  import ToolCard from "./ToolCard.svelte";

  type Props = {
    turn: ConversationTurn;
    loadTrace?: (turnId: string) => Promise<TurnEvent[]>;
  };
  let { turn, loadTrace }: Props = $props();

  const isUser = $derived(turn.role === "user");
  const isSystem = $derived(turn.role === "system");

  const safeEvents = $derived(turn.events ?? []);
  const safeAttachments = $derived(turn.attachments ?? []);
  const safeSteps = $derived(safeEvents.filter((ev) => ev.type === "step"));

  const showTraceToggle = $derived(!isUser && !isSystem && ((safeEvents.length > 0) || turn.has_trace === true));

  const isSyntheticId = $derived(
    turn.turn_id.startsWith("live-") || turn.turn_id.startsWith("sys-")
  );

  const canFetch = $derived(
    turn.has_trace === true && loadTrace !== undefined && !isSyntheticId
  );

  let traceOpen = $state(false);
  let traceLoading = $state(false);
  let traceEvents = $state<TurnEvent[] | null>(null);
  let traceError = $state(false);

  const resolvedTraceEvents = $derived(traceEvents ?? safeEvents);

  const traceToolBlocks = $derived(
    resolvedTraceEvents
      .filter((ev) => ev.type === "tool_use")
      .map((ev) => ({
        kind: "tool" as const,
        toolUseId: ev.tool_use_id ?? "",
        toolName: ev.tool_name ?? "",
        toolInput: ev.tool_input ?? "",
        result: resolvedTraceEvents.find((r) => r.type === "tool_result" && r.tool_use_id === ev.tool_use_id)?.text,
        isError: resolvedTraceEvents.find((r) => r.type === "tool_result" && r.tool_use_id === ev.tool_use_id)?.is_error,
      } satisfies Extract<ThreadBlock, { kind: "tool" }>)
    )
  );

  const traceOrphanResultBlocks = $derived(
    resolvedTraceEvents
      .filter((ev) =>
        ev.type === "tool_result" &&
        !resolvedTraceEvents.some((u) => u.type === "tool_use" && u.tool_use_id === ev.tool_use_id)
      )
      .map((ev) => ({
        kind: "tool" as const,
        toolUseId: ev.tool_use_id ?? "",
        toolName: ev.tool_name ?? "tool result",
        toolInput: "",
        result: ev.text,
        isError: ev.is_error,
      } satisfies Extract<ThreadBlock, { kind: "tool" }>)
    )
  );

  const traceThinkingEvents = $derived(
    resolvedTraceEvents.filter((ev) => ev.type === "thinking")
  );

  async function toggleTrace() {
    if (traceOpen) {
      traceOpen = false;
      return;
    }
    if (traceEvents !== null || !canFetch) {
      traceOpen = true;
      return;
    }
    traceLoading = true;
    traceError = false;
    try {
      traceEvents = await loadTrace!(turn.turn_id);
      traceOpen = true;
    } catch {
      traceError = true;
    } finally {
      traceLoading = false;
    }
  }

  async function retryTrace() {
    traceError = false;
    traceEvents = null;
    await toggleTrace();
  }
</script>

{#if isSystem}
  <div class="flex justify-center py-1">
    <div class="flex flex-col items-center gap-1 max-w-full">
      <div class="inline-flex items-start gap-1.5 rounded-2xl border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-3 py-1 text-xs text-black-700 dark:text-black-600 max-w-full">
        <svg viewBox="0 0 12 12" class="h-3 w-3 mt-0.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="6" cy="6" r="4.5"></circle>
          <path d="M6 4v2l1 1" stroke-linecap="round"></path>
        </svg>
        <span class="whitespace-pre-wrap break-words min-w-0">{turn.text}</span>
      </div>
      {#if safeSteps.length > 0}
        <div class="flex flex-col items-center gap-0.5 mt-0.5">
          {#each safeSteps as ev}
            <div class="inline-flex items-center gap-1 text-xs text-black-600 dark:text-black-500">
              <svg viewBox="0 0 12 12" class="h-2.5 w-2.5 text-green-500" fill="none" stroke="currentColor" stroke-width="1.5">
                <path d="M2 6l3 3 5-5" stroke-linecap="round" stroke-linejoin="round"></path>
              </svg>
              {ev.text ?? ""}
            </div>
          {/each}
        </div>
      {/if}
    </div>
  </div>
{:else if isUser}
  <div class="flex justify-end gap-2 group">
    <div class="flex flex-col items-end gap-1 max-w-[80%] min-w-0">
      {#if safeAttachments.length > 0}
        <div class="flex flex-wrap justify-end gap-1.5 max-w-full">
          {#each safeAttachments as attachment}
            {#if attachment.mime?.startsWith("image/")}
              <a
                href={attachment.url}
                target="_blank"
                rel="noopener"
                title={attachment.name}
                class="block rounded-xl overflow-hidden border border-white-300 dark:border-navy-600 shadow-sm bg-white-100 dark:bg-navy-800 hover:shadow-md transition-shadow"
              >
                <img src={attachment.url} alt={attachment.name} class="block max-h-56 max-w-[240px] object-contain bg-white-200 dark:bg-navy-900" />
              </a>
            {:else}
              <a
                href={attachment.url}
                target="_blank"
                rel="noopener"
                class="inline-flex items-center gap-2 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-xs text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors max-w-[240px] text-left"
              >
                <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-green-500" fill="none" stroke="currentColor" stroke-width="1.5">
                  <path d="M9 2H4a1 1 0 00-1 1v10a1 1 0 001 1h8a1 1 0 001-1V6L9 2z" stroke-linejoin="round"></path>
                  <path d="M9 2v4h4" stroke-linejoin="round"></path>
                </svg>
                <span class="truncate">{attachment.name}</span>
              </a>
            {/if}
          {/each}
        </div>
      {/if}
      {#if turn.text}
        <div class="rounded-2xl rounded-tr-sm bg-green-500 px-4 py-3 text-sm text-white-100 whitespace-pre-wrap break-words leading-relaxed shadow-sm">
          {@html linkifyText(turn.text)}
        </div>
      {/if}
    </div>
  </div>
{:else}
  <div class="flex justify-start group">
    <div class="flex flex-col gap-1.5 max-w-[92%] min-w-0">
      {#if showTraceToggle}
        <div class="flex flex-col gap-1">
          <button
            type="button"
            onclick={toggleTrace}
            class="inline-flex items-center gap-1.5 self-start text-[11px] text-black-500 dark:text-black-600 hover:text-black-700 dark:hover:text-black-500 transition-colors"
          >
            {#if traceLoading}
              <svg class="h-3 w-3 shrink-0 animate-spin" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
                <path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path>
              </svg>
              <span>loading…</span>
            {:else}
              <span>{traceOpen ? "⊖" : "⊕"}</span>
              <span>{traceOpen ? "hide trace" : "show trace ›"}</span>
            {/if}
          </button>

          {#if traceError}
            <div class="flex items-center gap-2 text-[11px] text-red-600 dark:text-red-400">
              <span>failed to load trace</span>
              <button
                type="button"
                onclick={retryTrace}
                class="underline hover:no-underline"
              >retry</button>
            </div>
          {/if}

          {#if traceOpen}
            <div class="flex flex-col gap-1 mt-0.5">
              {#each traceThinkingEvents as ev}
                <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs px-3 py-2 italic text-black-600 dark:text-black-700">
                  {ev.text ?? ""}
                </div>
              {/each}
              {#each traceToolBlocks as block}
                <ToolCard {block} />
              {/each}
              {#each traceOrphanResultBlocks as block}
                <ToolCard {block} />
              {/each}
            </div>
          {/if}
        </div>
      {/if}

      {#if turn.text}
        <div class="rounded-2xl rounded-tl-sm border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-4 py-3 text-sm text-black-900 dark:text-white-100 break-words leading-relaxed shadow-sm">
          {@html renderMarkdown(turn.text)}
          {#if turn.interrupted}
            <div class="mt-2 flex items-center gap-1.5 border-t border-white-300 dark:border-navy-600 pt-2">
              <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-amber-500" fill="none" stroke="currentColor" stroke-width="1.5">
                <path d="M8 2L1.5 13.5h13L8 2z" stroke-linejoin="round"></path>
                <path d="M8 6v4M8 11.5v.5" stroke-linecap="round"></path>
              </svg>
              <span class="text-xs text-amber-600 dark:text-amber-400">Interrupted — response was cut off</span>
            </div>
          {:else if turn.truncated}
            <p class="mt-2 text-xs text-black-600 dark:text-black-700 italic border-t border-white-300 dark:border-navy-600 pt-2">Output truncated — see raw.jsonl for full content.</p>
          {/if}
        </div>
      {/if}

      {#if !turn.text && turn.interrupted}
        <div class="rounded-2xl rounded-tl-sm border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-4 py-3 shadow-sm">
          <div class="flex items-center gap-1.5">
            <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-amber-500" fill="none" stroke="currentColor" stroke-width="1.5">
              <path d="M8 2L1.5 13.5h13L8 2z" stroke-linejoin="round"></path>
              <path d="M8 6v4M8 11.5v.5" stroke-linecap="round"></path>
            </svg>
            <span class="text-xs text-amber-700 dark:text-amber-300">Interrupted — response was cut off</span>
          </div>
        </div>
      {/if}
    </div>
  </div>
{/if}
