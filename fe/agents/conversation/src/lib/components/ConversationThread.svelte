<script lang="ts">
  import { onMount } from "svelte";
  import type { ConversationTurn, LiveTurn, TypingState, ThreadBlock, TurnEvent } from "../types/agents.js";
  import { renderMarkdown } from "../markdown.js";
  import ThreadMessage from "./ThreadMessage.svelte";
  import ToolCard from "./ToolCard.svelte";

  type Props = {
    turns: ConversationTurn[];
    live: LiveTurn | null;
    typing: TypingState;
    loadTrace?: (turnId: string) => Promise<TurnEvent[]>;
  };

  let { turns, live, typing, loadTrace }: Props = $props();

  let containerEl: HTMLElement | undefined = $state();

  function typingLabel(substate?: string): string {
    if (!substate || substate === "thinking") return "thinking…";
    if (substate === "spawning") return "spawning…";
    return `running ${substate}…`;
  }

  const liveToolBlocks = $derived(
    live
      ? live.blocks.filter((b): b is Extract<ThreadBlock, { kind: "tool" }> => b.kind === "tool")
      : []
  );

  onMount(() => {
    if (!containerEl) return;
    containerEl.addEventListener("click", (e: MouseEvent) => {
      const btn = (e.target as Element).closest<HTMLElement>("[data-copy-code]");
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
  {#each turns as turn, i (turn.turn_id ? turn.turn_id + "-" + i : "turn-" + i)}
    <ThreadMessage {turn} {loadTrace} />
  {/each}

  {#if live}
    <div class="flex justify-start">
      <div class="flex flex-col gap-1.5 max-w-[92%] min-w-0">
        {#if liveToolBlocks.length > 0}
          <div class="flex flex-col gap-1">
            {#each liveToolBlocks as block}
              <ToolCard {block} />
            {/each}
          </div>
        {/if}
        {#if live.blocks.some((b) => b.kind === "thinking")}
          {#each live.blocks.filter((b) => b.kind === "thinking") as block}
            <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs px-3 py-2 italic text-black-600 dark:text-black-700">
              {(block as Extract<ThreadBlock, { kind: "thinking" }>).text}
            </div>
          {/each}
        {/if}
        {#if live.text}
          <div class="rounded-2xl rounded-tl-sm border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-4 py-3 text-sm text-black-900 dark:text-white-100 break-words leading-relaxed shadow-sm">
            {@html renderMarkdown(live.text)}
          </div>
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
