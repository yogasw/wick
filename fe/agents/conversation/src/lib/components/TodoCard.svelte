<script lang="ts">
  import type { TodoItemWithSteps, TodoSubstep } from "../todoGroups.js";
  import { todoProgress, itemLabel } from "../todoGroups.js";
  import ToolCard from "./ToolCard.svelte";

  type Props = {
    items: TodoItemWithSteps[];
    /** What's actively running right now (e.g. "running wick_execute…"),
        shown inline under the in_progress item instead of as a separate
        floating indicator — only meaningful for the LIVE turn. */
    currentActivity?: string;
  };
  let { items, currentActivity }: Props = $props();

  const plainItems = $derived(items.map((s) => s.item));
  const progress = $derived(todoProgress(plainItems));
  const pct = $derived(progress.total > 0 ? Math.round((progress.done / progress.total) * 100) : 0);

  let collapsed = $state(false);
  let expandedKeys = $state<Set<string>>(new Set());

  function itemKey(s: TodoItemWithSteps, i: number): string {
    return s.item.id?.trim() || `${i}:${itemLabel(s.item)}`;
  }

  function toggleItem(key: string) {
    const next = new Set(expandedKeys);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    expandedKeys = next;
  }

  function substepMark(status: string): "done" | "active" | "pending" {
    if (status === "completed") return "done";
    if (status === "in_progress") return "active";
    return "pending";
  }

  // Sum of each related tool call's own duration (not wall-clock span of
  // the whole item — tool calls don't overlap within one item today, so
  // sum == span, but summing is correct even if that ever changes).
  function totalDurationMs(s: TodoItemWithSteps): number {
    let total = 0;
    for (const b of s.relatedBlocks) {
      if (b.kind === "tool" && b.startedAt && b.endedAt && b.endedAt > b.startedAt) {
        total += b.endedAt - b.startedAt;
      }
    }
    return total;
  }

  function fmtTotalDuration(ms: number): string {
    if (ms <= 0) return "";
    const s = Math.round(ms / 1000);
    if (s < 60) return `${s}s`;
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m ${s % 60}s`;
    return `${Math.floor(m / 60)}h ${m % 60}m`;
  }
</script>

<div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs">
  <button
    type="button"
    onclick={() => (collapsed = !collapsed)}
    class="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
  >
    <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-green-600 dark:text-green-400" fill="none" stroke="currentColor" stroke-width="1.5">
      <path d="M3 4h10M3 8h10M3 12h6" stroke-linecap="round"></path>
    </svg>
    <span class="font-mono font-medium text-black-900 dark:text-white-100">todo</span>
    {#if items.length > 0}
      <span class="ml-auto text-[10px] text-black-500 dark:text-black-600">{progress.done}/{progress.total} done</span>
    {/if}
    <svg
      data-chevron
      viewBox="0 0 16 16"
      class="h-3 w-3 shrink-0 text-black-500 transition-transform"
      style={collapsed ? "transform:rotate(-90deg)" : ""}
      fill="none"
      stroke="currentColor"
      stroke-width="1.5"
    >
      <path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"></path>
    </svg>
  </button>
  {#if !collapsed}
    {#if items.length > 0}
      <div class="border-t border-white-300 dark:border-navy-600 px-3 pt-2 flex items-center gap-2">
        <div class="h-1.5 flex-1 rounded-full bg-white-300 dark:bg-navy-600 overflow-hidden">
          <div class="h-full rounded-full bg-green-500 transition-all" style={`width:${pct}%`}></div>
        </div>
        <span class="text-[10px] text-black-500 dark:text-black-600 shrink-0">{pct}%</span>
      </div>
      <ul class="flex flex-col divide-y divide-white-300 dark:divide-navy-600 mt-1">
        {#each items as s, i}
          {@const key = itemKey(s, i)}
          {@const hasSubsteps = (s.item.substeps?.length ?? 0) > 0}
          {@const hasExpandable = s.relatedBlocks.length > 0 || !!s.item.description || hasSubsteps}
          {@const isOpen = expandedKeys.has(key)}
          {@const isActive = s.item.status === "in_progress"}
          <li>
            <button
              type="button"
              disabled={!hasExpandable}
              onclick={() => hasExpandable && toggleItem(key)}
              class="flex w-full items-start gap-2 px-3 py-1.5 text-left {hasExpandable ? 'hover:bg-white-200 dark:hover:bg-navy-700 cursor-pointer' : 'cursor-default'}"
            >
              {#if s.item.status === "completed"}
                <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 mt-0.5 text-green-600 dark:text-green-400" fill="none" stroke="currentColor" stroke-width="1.5">
                  <circle cx="8" cy="8" r="6"></circle>
                  <path d="M5.5 8l1.8 1.8L10.5 6" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
              {:else if isActive}
                <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 mt-0.5 animate-spin text-green-600 dark:text-green-400" fill="none" stroke="currentColor" stroke-width="1.5">
                  <path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path>
                </svg>
              {:else}
                <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 mt-0.5 text-black-500 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5">
                  <circle cx="8" cy="8" r="6"></circle>
                </svg>
              {/if}
              <span class="flex-1 min-w-0">
                <span
                  class="block {s.item.status === 'completed'
                    ? 'text-black-500 dark:text-black-600 line-through'
                    : isActive
                      ? 'text-black-900 dark:text-white-100 font-medium'
                      : 'text-black-700 dark:text-black-600'}"
                >{itemLabel(s.item)}</span>
                {#if isActive && currentActivity}
                  <!-- Replaces the old separate floating "running X…" indicator —
                       shown inline under the active item so hiding the trace
                       doesn't lose "what's happening right now". -->
                  <span class="mt-0.5 flex items-center gap-1 text-[11px] italic text-black-600 dark:text-black-700">
                    <svg viewBox="0 0 16 16" class="h-2.5 w-2.5 shrink-0 animate-spin" fill="none" stroke="currentColor" stroke-width="1.5">
                      <path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path>
                    </svg>
                    {currentActivity}
                  </span>
                {/if}
              </span>
              {#if hasExpandable}
                {#if s.relatedBlocks.length > 0}
                  {@const dur = fmtTotalDuration(totalDurationMs(s))}
                  <span class="text-[10px] text-black-500 dark:text-black-600 shrink-0">
                    {s.relatedBlocks.length} step{s.relatedBlocks.length === 1 ? "" : "s"}{dur ? ` · ${dur}` : ""}
                  </span>
                {/if}
                <svg
                  viewBox="0 0 16 16"
                  class="h-3 w-3 shrink-0 mt-0.5 text-black-500 transition-transform"
                  style={isOpen ? "transform:rotate(90deg)" : ""}
                  fill="none"
                  stroke="currentColor"
                  stroke-width="1.5"
                >
                  <path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
              {/if}
            </button>
            {#if hasExpandable && isOpen}
              <div class="flex flex-col gap-2 px-3 pb-2 pl-8">
                {#if s.item.description}
                  <p class="text-[11px] text-black-600 dark:text-black-700">{s.item.description}</p>
                {/if}
                {#if hasSubsteps}
                  <ul class="flex flex-col gap-1">
                    {#each s.item.substeps ?? [] as sub}
                      {@const m = substepMark(sub.status)}
                      <li class="flex items-center gap-1.5 text-[11px]">
                        {#if m === "done"}
                          <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-green-600 dark:text-green-400" fill="none" stroke="currentColor" stroke-width="1.5">
                            <circle cx="8" cy="8" r="6"></circle>
                            <path d="M5.5 8l1.8 1.8L10.5 6" stroke-linecap="round" stroke-linejoin="round"></path>
                          </svg>
                        {:else if m === "active"}
                          <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 animate-spin text-green-600 dark:text-green-400" fill="none" stroke="currentColor" stroke-width="1.5">
                            <path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path>
                          </svg>
                        {:else}
                          <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-black-500 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5">
                            <circle cx="8" cy="8" r="6"></circle>
                          </svg>
                        {/if}
                        <span class={m === "done" ? "text-black-500 dark:text-black-600 line-through" : m === "active" ? "text-black-900 dark:text-white-100 font-medium" : "text-black-700 dark:text-black-600"}>{sub.step}</span>
                      </li>
                    {/each}
                  </ul>
                {/if}
                {#each s.relatedBlocks as block}
                  {#if block.kind === "thinking"}
                    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs px-3 py-2 italic text-black-600 dark:text-black-700 whitespace-pre-wrap break-words">
                      {block.text}
                    </div>
                  {:else if block.kind === "raw"}
                    <details class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs">
                      <summary class="cursor-pointer px-3 py-2 text-black-600 dark:text-black-700 select-none">Raw event</summary>
                      <pre class="px-3 pb-2 overflow-x-auto text-[11px] text-black-700 dark:text-black-600 whitespace-pre-wrap break-words">{block.text}</pre>
                    </details>
                  {:else}
                    <ToolCard {block} />
                  {/if}
                {/each}
              </div>
            {/if}
          </li>
        {/each}
      </ul>
    {:else}
      <div class="border-t border-white-300 dark:border-navy-600">
        <p class="px-3 py-2 text-black-500 dark:text-black-600 italic">no items</p>
      </div>
    {/if}
  {/if}
</div>
