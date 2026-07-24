<script lang="ts">
  import type { TodoItemWithSteps } from "../todoGroups.js";
  import { todoProgress } from "../todoGroups.js";
  import ToolCard from "./ToolCard.svelte";

  type Props = { items: TodoItemWithSteps[] };
  let { items }: Props = $props();

  const plainItems = $derived(items.map((s) => s.item));
  const progress = $derived(todoProgress(plainItems));
  const pct = $derived(progress.total > 0 ? Math.round((progress.done / progress.total) * 100) : 0);

  let collapsed = $state(false);
  let expandedKeys = $state<Set<string>>(new Set());

  function itemKey(s: TodoItemWithSteps, i: number): string {
    return s.item.id?.trim() || `${i}:${s.item.step}`;
  }

  function toggleItem(key: string) {
    const next = new Set(expandedKeys);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    expandedKeys = next;
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
          {@const hasSteps = s.relatedBlocks.length > 0}
          {@const isOpen = expandedKeys.has(key)}
          <li>
            <button
              type="button"
              disabled={!hasSteps}
              onclick={() => hasSteps && toggleItem(key)}
              class="flex w-full items-start gap-2 px-3 py-1.5 text-left {hasSteps ? 'hover:bg-white-200 dark:hover:bg-navy-700 cursor-pointer' : 'cursor-default'}"
            >
              {#if s.item.status === "completed"}
                <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 mt-0.5 text-green-600 dark:text-green-400" fill="none" stroke="currentColor" stroke-width="1.5">
                  <circle cx="8" cy="8" r="6"></circle>
                  <path d="M5.5 8l1.8 1.8L10.5 6" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
              {:else if s.item.status === "in_progress"}
                <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 mt-0.5 animate-spin text-green-600 dark:text-green-400" fill="none" stroke="currentColor" stroke-width="1.5">
                  <path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path>
                </svg>
              {:else}
                <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 mt-0.5 text-black-500 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5">
                  <circle cx="8" cy="8" r="6"></circle>
                </svg>
              {/if}
              <span
                class="flex-1 {s.item.status === 'completed'
                  ? 'text-black-500 dark:text-black-600 line-through'
                  : s.item.status === 'in_progress'
                    ? 'text-black-900 dark:text-white-100 font-medium'
                    : 'text-black-700 dark:text-black-600'}"
              >{s.item.step}</span>
              {#if hasSteps}
                <span class="text-[10px] text-black-500 dark:text-black-600 shrink-0">{s.relatedBlocks.length} step{s.relatedBlocks.length === 1 ? "" : "s"}</span>
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
            {#if hasSteps && isOpen}
              <div class="flex flex-col gap-1 px-3 pb-2 pl-8">
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
