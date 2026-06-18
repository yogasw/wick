<script lang="ts">
  import { onMount } from "svelte";
  import type { ConversationTurn, LiveTurn, TypingState, ThreadBlock, TurnEvent } from "../types/agents.js";
  import { renderLive } from "../richRender.js";
  import ThreadMessage from "./ThreadMessage.svelte";
  import ToolCard from "./ToolCard.svelte";

  type Props = {
    turns: ConversationTurn[];
    live: LiveTurn | null;
    typing: TypingState;
    loadTrace?: (turnId: string) => Promise<TurnEvent[]>;
    onOpenPath?: (path: string) => void;
  };

  let { turns, live, typing, loadTrace, onOpenPath }: Props = $props();

  let containerEl: HTMLElement | undefined = $state();

  function typingLabel(substate?: string): string {
    if (!substate || substate === "thinking") return "thinking…";
    if (substate === "spawning") return "spawning…";
    return `running ${substate}…`;
  }

  let liveTraceOpen = $state(false);

  const isEmpty = $derived(turns.length === 0 && !live && !typing.active);

  onMount(() => {
    if (!containerEl) return;
    containerEl.addEventListener("click", (e: MouseEvent) => {
      const target = e.target as Element;
      const link = target.closest<HTMLElement>("[data-chat-path]");
      if (link) {
        e.preventDefault();
        const path = link.dataset.chatPath ?? "";
        if (path) onOpenPath?.(path);
        return;
      }
      const btn = target.closest<HTMLElement>("[data-copy-code]");
      if (!btn) return;
      const code = btn.dataset.code ?? "";
      navigator.clipboard.writeText(code).then(() => {
        const prev = btn.textContent;
        btn.textContent = "Copied";
        setTimeout(() => { btn.textContent = prev; }, 1500);
      }).catch(() => {});
    });
  });
</script>

<div bind:this={containerEl} class="flex flex-col gap-3 px-4 py-3">
  {#if isEmpty}
    <div class="flex flex-col items-center justify-center py-16 text-center gap-1">
      <p class="text-sm font-medium text-black-700 dark:text-black-600">No messages yet</p>
      <p class="text-xs text-black-600 dark:text-black-700">Send a message to start.</p>
    </div>
  {/if}
  {#each turns as turn, i (turn.turn_id ? turn.turn_id + "-" + i : "turn-" + i)}
    <ThreadMessage {turn} {loadTrace} />
  {/each}

  {#if live}
    <div class="flex justify-start">
      <div class="flex flex-col gap-1.5 max-w-[92%] min-w-0">
        {#if live.blocks.length > 0}
          <button
            type="button"
            data-live-trace-toggle
            class="self-start inline-flex items-center gap-1.5 text-xs text-black-600 dark:text-black-500 hover:text-black-800 dark:hover:text-black-300 transition-colors"
            onclick={() => (liveTraceOpen = !liveTraceOpen)}
          >
            <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
              <circle cx="8" cy="8" r="5.5"></circle><path d="M6 8h4M8 6v4" stroke-linecap="round"></path>
            </svg>
            <span>{liveTraceOpen ? "hide trace" : "show trace"}</span>
            {#if !liveTraceOpen}
              <span class="text-black-500 dark:text-black-600">· {live.blocks.length} step{live.blocks.length === 1 ? "" : "s"}</span>
            {/if}
            <svg viewBox="0 0 12 12" class="h-3 w-3 shrink-0 transition-transform {liveTraceOpen ? 'rotate-90' : ''}" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
              <path d="M4.5 3l3 3-3 3" stroke-linecap="round" stroke-linejoin="round"></path>
            </svg>
          </button>
          {#if liveTraceOpen}
            <div class="flex flex-col gap-1">
              {#each live.blocks as block, bi (bi)}
                {#if block.kind === "tool"}
                  <ToolCard block={block as Extract<ThreadBlock, { kind: "tool" }>} />
                {:else if block.kind === "thinking"}
                  <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs px-3 py-2 italic text-black-600 dark:text-black-700">
                    {(block as Extract<ThreadBlock, { kind: "thinking" }>).text}
                  </div>
                {/if}
              {/each}
            </div>
          {/if}
        {/if}
        {#if live.text}
          <!-- renderLive owns innerHTML (no {@html}) so streaming tokens don't
               wipe already-rendered diagrams — prevents text↔image flicker. -->
          <div use:renderLive={live.text} class="rounded-2xl rounded-tl-sm bg-white-200 dark:bg-navy-800 px-4 py-3 text-sm text-black-900 dark:text-white-100 break-words leading-relaxed shadow-sm"></div>
        {/if}
      </div>
    </div>
  {/if}

  {#if typing.active}
    <div class="flex justify-start items-end">
      <div class="rounded-2xl rounded-tl-sm border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-4 py-2.5">
        <div class="flex items-center gap-2 text-xs text-black-600 dark:text-black-700">
          <svg class="h-3 w-3 shrink-0 animate-spin text-green-500" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path>
          </svg>
          <span class="italic">{typingLabel(typing.substate)}</span>
        </div>
      </div>
    </div>
  {/if}
</div>
