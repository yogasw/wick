<script lang="ts">
  /* TraceBody renders one tool call input or tool result: the backend's
     Display when the event has one, the TS classifier's otherwise. Every
     block gets the same frame — a height cap with Expand, a Raw toggle
     (the untouched event text) and Copy — so a long result cannot wreck
     the thread and the original is always one click away. */
  import { getTraceRenderer } from "./registry.js";
  import { classifyCall, classifyResult } from "./classify.js";
  import { rawPreview, truncationNote } from "./format.js";
  import { skillDisplay } from "./skill.js";
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
    /* The matching call's input text (result side): a read's offset/limit,
       so a partial read of a SKILL.md says so on its card. */
    callInput?: string;
    /* The payload file said the store cut it (TurnEventPayload.truncated). */
    truncated?: boolean;
    ctx?: TraceContext;
    maxHeight?: number;
  };
  let { display, raw = "", toolName = "", call = false, isError = false, callDisplay, callInput, truncated = false, ctx, maxHeight = 320 }: Props = $props();

  let showRaw = $state(false);

  const classify = () => (call ? classifyCall(toolName, raw) : classifyResult(toolName, raw, isError, callDisplay));

  const d = $derived.by<TraceDisplay>(() => {
    let out = display ?? classify();
    // A Large event's index display is header-only: the body arrives with
    // the payload. Keep the backend's kind and card fields, take the body.
    if (!out.body && !out.command && !isBinaryKind(out.kind) && raw) {
      out = { ...out, body: classify().body ?? raw };
    }
    // Live turn (inflight / SSE): the backend display has no blob_ref yet
    // and the raw text still holds the base64 — borrow it from the
    // classifier so the chip can decode on click.
    if (raw && needsData(out)) {
      const c = classify();
      const bins = [c, ...(c.parts ?? [])].filter((p) => isBinaryKind(p.kind) && p.data);
      let i = 0;
      const fill = (p: TraceDisplay): TraceDisplay =>
        isBinaryKind(p.kind) && !p.blob_ref && !p.data && bins[i] ? { ...p, data: bins[i++].data } : p;
      out = fill(out);
      if (out.parts) out = { ...out, parts: out.parts.map(fill) };
    }
    if (truncated && !out.truncated) out = { ...out, truncated: true };
    // A read of a SKILL.md renders as a skill card, whatever kind the
    // backend gave the file text.
    if (!call) out = skillDisplay(out, toolName, callDisplay, callInput) ?? out;
    return out;
  });

  function needsData(d: TraceDisplay): boolean {
    return [d, ...(d.parts ?? [])].some((p) => isBinaryKind(p.kind) && !p.blob_ref && !p.data && !p.too_large);
  }

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
      class="rounded border px-1.5 py-0.5 text-[10px] font-medium hover:bg-white-200 dark:hover:bg-navy-700 {showRaw ? 'border-link-400 text-link-400' : 'border-white-300 dark:border-navy-600 text-black-800 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100'}"
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
