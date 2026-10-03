<script lang="ts">
  /* TraceBody renders one tool call input or tool result: the backend's
     Display when the event has one, the TS classifier's otherwise. Every
     block gets the same frame — a height cap with Expand, a Raw toggle
     (the untouched event text) and Copy — so a long result cannot wreck
     the thread and the original is always one click away. */
  import { getTraceRenderer } from "./registry.js";
  import { classifyCall, classifyResult } from "./classify.js";
  import { rawPreview, truncationNote } from "./format.js";
  import { isBinaryKind, type TraceContext, type TraceDisplay } from "./types.js";
  import ScrollBox from "./ScrollBox.svelte";
  import CopyButton from "./blocks/CopyButton.svelte";

  type Props = {
    display?: TraceDisplay | null;
    /* The event text as recorded — what Raw shows and what the classifier
       reads when display is missing. */
    raw?: string;
    toolName?: string;
    call?: boolean;
    isError?: boolean;
    /* The matching call's display (result side): gives read results the
       call's path and language. */
    callDisplay?: TraceDisplay | null;
    /* The payload file said the store cut it (TurnEventPayload.truncated). */
    truncated?: boolean;
    ctx?: TraceContext;
    maxHeight?: number;
  };
  let { display, raw = "", toolName = "", call = false, isError = false, callDisplay, truncated = false, ctx, maxHeight = 320 }: Props = $props();

  let showRaw = $state(false);

  const classify = () => (call ? classifyCall(toolName, raw) : classifyResult(toolName, raw, isError, callDisplay));

  const d = $derived.by<TraceDisplay>(() => {
    let out = display ?? classify();
    // A Large event's index display is header-only: the body arrives with
    // the payload. Keep the backend's kind and card fields, take the body.
    if (!out.body && !out.command && !isBinaryKind(out.kind) && raw) {
      out = { ...out, body: classify().body ?? raw };
    }
    if (truncated && !out.truncated) out = { ...out, truncated: true };
    return out;
  });

  const binary = $derived(isBinaryKind(d.kind));
  const Renderer = $derived(getTraceRenderer(d.kind));
  const rawText = $derived(raw || d.body || "");
  const banner = $derived(d.truncated && !binary ? truncationNote(d.body ?? "", d.original_bytes) : null);
  const copyValue = $derived(showRaw ? rawText : (d.command ?? d.body ?? rawText));

</script>

<div data-trace-body data-kind={d.kind} class="text-xs">
  <div class="flex items-center gap-1 px-2 pt-1">
    <span class="mr-auto text-[10px] uppercase tracking-wide text-black-500 dark:text-black-600">{d.kind}{d.lang && d.lang !== d.kind ? ` · ${d.lang}` : ""}</span>
    <button
      type="button"
      data-trace-raw
      aria-pressed={showRaw}
      onclick={(e) => { e.stopPropagation(); showRaw = !showRaw; }}
      class="rounded px-1.5 py-0.5 text-[10px] font-medium hover:bg-white-200 dark:hover:bg-navy-700 {showRaw ? 'text-link-400' : 'text-black-600 dark:text-black-500'}"
    >Raw</button>
    {#if !binary || showRaw}<CopyButton text={copyValue} />{/if}
  </div>
  {#if showRaw}
    <ScrollBox {maxHeight}>
      <pre data-trace-raw-text class="px-3 py-2 font-mono text-[11px] leading-relaxed whitespace-pre-wrap break-all text-black-800 dark:text-black-600">{rawPreview(rawText, binary || !!d.parts?.length)}</pre>
    </ScrollBox>
  {:else}
    {#if binary}
      <Renderer display={d} {ctx} />
    {:else}
      <ScrollBox {maxHeight}>
        <Renderer display={d} {ctx} />
      </ScrollBox>
    {/if}
    {#each d.parts ?? [] as p, i (i)}
      {@const Part = getTraceRenderer(p.kind)}
      <Part display={p} {ctx} />
    {/each}
  {/if}
  {#if banner}
    <p data-trace-truncated class="px-3 py-1.5 text-[10px] italic text-amber-700 dark:text-amber-300">{banner}</p>
  {/if}
</div>
