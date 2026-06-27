<script lang="ts">
  import type { ConversationTurn, ThreadBlock, TurnEvent } from "../types/agents.js";
  import { renderMarkdown, linkifyText } from "../markdown.js";
  import { turnTime } from "../timeFormat.js";
  import { enrich } from "../richRender.js";
  import ToolCard from "./ToolCard.svelte";
  import ArtifactGallery from "./ArtifactGallery.svelte";
  import MediaLightbox from "./MediaLightbox.svelte";

  type Props = {
    turn: ConversationTurn;
    loadTrace?: (turnId: string) => Promise<TurnEvent[]>;
  };
  let { turn, loadTrace }: Props = $props();

  const isUser = $derived(turn.role === "user");
  const isSystem = $derived(turn.role === "system");

  /* Per-bubble timestamp is just the clock (WhatsApp shows only HH:mm inside
     each bubble; the day/date lives in the centered separator the parent
     renders). Reads `ts` (RFC3339 from history) first, falls back to
     `timestamp` (epoch ms on client-built live turns). */
  const stamp = $derived(turnTime(turn));

  const safeEvents = $derived(turn.events ?? []);
  const safeAttachments = $derived(turn.attachments ?? []);
  const safeArtifacts = $derived(turn.artifacts ?? []);
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

  type LightboxItem = { url: string; name: string; kind: "image" | "pdf" | "html" | "markdown" | "text" | "file"; sourceUrl?: string };
  /* The viewer is always a gallery. A single attachment / artifact opens as a
     one-element gallery; an image-card grid opens with every card so the user
     can page through them (← / → / prev-next). */
  let lightbox = $state<{ items: LightboxItem[]; index: number } | null>(null);

  /* single-item entry point for attachments + artifacts (ArtifactGallery
     onOpen passes one item) — a one-element gallery. */
  function openLightbox(item: LightboxItem) {
    lightbox = { items: [item], index: 0 };
  }

  function openGallery(items: LightboxItem[], index: number) {
    if (items.length) lightbox = { items, index };
  }

  function closeLightbox() {
    lightbox = null;
  }

  /* The image-card grid is rendered imperatively by richRender (outside Svelte),
     so a clicked card can't call openGallery directly. It dispatches a
     `wick-imagecard-open` CustomEvent that bubbles to this bubble's root; we
     catch it here and open the gallery with the card's siblings. */
  function onImageCardOpen(e: Event) {
    const d = (e as CustomEvent).detail as { items?: LightboxItem[]; index?: number } | undefined;
    if (d?.items?.length) openGallery(d.items, d.index ?? 0);
  }

  const resolvedTraceEvents = $derived(traceEvents ?? safeEvents);

  /* Walk the trace in order, coalescing consecutive thinking deltas into one
     block so streamed thinking_delta fragments render as a single bubble
     instead of one bubble per delta. Tool calls break a thinking run, so
     chronological think→tool→think ordering is preserved. */
  const traceBlocks = $derived.by<ThreadBlock[]>(() => {
    const blocks: ThreadBlock[] = [];
    let thinking: string | null = null;
    const flush = () => {
      if (thinking !== null) {
        blocks.push({ kind: "thinking", text: thinking });
        thinking = null;
      }
    };
    for (const ev of resolvedTraceEvents) {
      if (ev.type === "thinking") {
        thinking = (thinking ?? "") + (ev.text ?? "");
      } else if (ev.type === "tool_use") {
        flush();
        const res = resolvedTraceEvents.find((r) => r.type === "tool_result" && r.tool_use_id === ev.tool_use_id);
        blocks.push({
          kind: "tool",
          toolUseId: ev.tool_use_id ?? "",
          toolName: ev.tool_name ?? "",
          toolInput: ev.tool_input ?? "",
          result: res?.text,
          isError: res?.is_error,
        });
      } else if (ev.type === "tool_result") {
        const paired = resolvedTraceEvents.some((u) => u.type === "tool_use" && u.tool_use_id === ev.tool_use_id);
        if (paired) {
          continue;
        }
        flush();
        blocks.push({
          kind: "tool",
          toolUseId: ev.tool_use_id ?? "",
          toolName: ev.tool_name ?? "tool result",
          toolInput: "",
          result: ev.text,
          isError: ev.is_error,
        });
      }
    }
    flush();
    return blocks;
  });

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
  <div class="flex min-w-0 max-w-full justify-end gap-2 group">
    <div class="flex flex-col items-end gap-1 max-w-[80%] min-w-0">
      {#if safeAttachments.length > 0}
        <div class="flex flex-wrap justify-end gap-1.5 max-w-full">
          {#each safeAttachments as attachment}
            {#if attachment.mime?.startsWith("image/")}
              <button
                type="button"
                data-lightbox-trigger
                title={attachment.name}
                onclick={() => openLightbox({ url: attachment.url, name: attachment.name, kind: "image" })}
                class="block rounded-xl overflow-hidden border border-white-300 dark:border-navy-600 shadow-sm bg-white-100 dark:bg-navy-800 hover:shadow-md transition-shadow cursor-zoom-in"
              >
                <img src={attachment.url} alt={attachment.name} class="block max-h-56 max-w-[240px] object-contain bg-white-200 dark:bg-navy-900" />
              </button>
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
        {#if stamp}
          <span class="text-[10px] leading-none text-black-500 dark:text-black-600 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">{stamp}</span>
        {/if}
        <div class="min-w-0 max-w-full overflow-hidden rounded-2xl rounded-tr-sm bg-green-500 px-4 py-2.5 text-sm text-white-100 whitespace-pre-wrap [overflow-wrap:anywhere] leading-relaxed shadow-sm">
          {@html linkifyText(turn.text)}
        </div>
      {/if}
    </div>
  </div>
{:else}
  <div class="flex justify-start group">
    <div class="flex flex-col gap-1.5 w-full max-w-full min-w-0">
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
            <div class="flex flex-col gap-1 mt-0.5" data-trace-blocks>
              {#each traceBlocks as block}
                {#if block.kind === "thinking"}
                  <div data-thinking-block class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs px-3 py-2 italic text-black-600 dark:text-black-700 whitespace-pre-wrap break-words">
                    {block.text}
                  </div>
                {:else}
                  <ToolCard {block} />
                {/if}
              {/each}
            </div>
          {/if}
        </div>
      {/if}

      {#if turn.text}
        {#if stamp}
          <span class="self-start text-[10px] leading-none text-black-500 dark:text-black-600">{stamp}</span>
        {/if}
        <div use:enrich={turn.text} onwick-imagecard-open={onImageCardOpen} class="rounded-2xl rounded-tl-sm bg-white-200 dark:bg-navy-800 px-4 py-3 text-sm text-black-900 dark:text-white-100 break-words leading-relaxed shadow-sm">
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

      {#if safeArtifacts.length > 0}
        <ArtifactGallery artifacts={safeArtifacts} onOpen={openLightbox} />
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

<MediaLightbox items={lightbox?.items ?? null} index={lightbox?.index ?? 0} onClose={closeLightbox} />
